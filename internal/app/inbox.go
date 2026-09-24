package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"focusflow/internal/store"
	"os"
	"time"
)

// inboxPollInterval is how often `inbox pop --wait` re-checks the mailbox.
// A var (not const) so tests can shrink it instead of sleeping real time.
var inboxPollInterval = 500 * time.Millisecond

// cmdInbox dispatches `focusflow inbox [list|pop|read|stats] ...`. A bare
// `focusflow inbox` (or one starting with a flag) defaults to list.
func cmdInbox(args []string) int {
	if len(args) > 0 && !looksLikeFlag(args[0]) {
		switch args[0] {
		case "pop":
			return cmdInboxPop(args[1:])
		case "read":
			return cmdInboxRead(args[1:])
		case "list":
			return cmdInboxList(args[1:])
		case "stats":
			return cmdInboxStats(args[1:])
		default:
			fmt.Fprintf(os.Stderr, "error: unknown inbox subcommand %q\n", args[0])
			return 2
		}
	}
	return cmdInboxList(args)
}

// resolveMailbox picks which mailbox to read: an explicit --as assignee
// (e.g. "user", the human's aggregate queue across all tasks), or — with
// none given — the task bound to the current session, falling back to
// the human's own mailbox for a dispatch/unbound session.
func resolveMailbox(db *sql.DB, as string) store.MailboxFilter {
	if as != "" {
		return store.MailboxFilter{Assignee: as}
	}
	if slug := resolveCurrentTaskSlug(db); slug != "" {
		return store.MailboxFilter{TaskSlug: slug}
	}
	return store.MailboxFilter{Assignee: "user"}
}

// cmdInboxList implements `focusflow inbox [--as <assignee>] [--all] [--json]`.
func cmdInboxList(args []string) int {
	fs := flagSet("inbox")
	as := fs.String("as", "", "assignee-level mailbox to read (e.g. user)")
	taskMailbox := fs.String("task", "", "read this task's own mailbox (the dashboard uses it for delivery receipts)")
	all := fs.Bool("all", false, "include already-read messages")
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		return 1
	}
	defer db.Close()

	if *as != "" && *taskMailbox != "" {
		fmt.Fprintln(os.Stderr, "error: --as and --task are mutually exclusive")
		return 2
	}
	mailbox := resolveMailbox(db, *as)
	inspecting := *taskMailbox != ""
	if inspecting {
		mailbox = store.MailboxFilter{TaskSlug: *taskMailbox}
	}
	msgs, err := store.ListBusMessages(db, mailbox, *all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	// Listing a task's OWN mailbox (from inside its session) is a delivery:
	// the session has now seen it. Inspecting via --task is not.
	if *as == "" && !inspecting && mailbox.TaskSlug != "" {
		if _, err := store.MarkDelivered(db, mailbox); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}
	if *jsonOut {
		printMessagesJSON(msgs)
	} else {
		printMessagesTable(msgs)
	}
	return 0
}

// cmdInboxPop implements the consumption API:
// `focusflow inbox pop [--wait] [--timeout <s>] [--as <assignee>] [--json] [--keep-unread]`.
// It atomically consumes (or, with --keep-unread, peeks at) the oldest
// unread message in the mailbox. With --wait it polls until a message
// arrives or --timeout elapses; per the skill (§4.18), timing out is silent
// with --json (nothing printed, exit 0) so a polling loop like
// `while true; do focusflow inbox pop --wait --timeout 300 --json || true; done`
// never emits a spurious event.
func cmdInboxPop(args []string) int {
	fs := flagSet("inbox pop")
	wait := fs.Bool("wait", false, "block until a message arrives or --timeout elapses")
	timeoutSec := fs.Int("timeout", 30, "seconds to wait when --wait is set")
	as := fs.String("as", "", "assignee-level mailbox to read (e.g. user)")
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	keepUnread := fs.Bool("keep-unread", false, "peek without acking — wakes on new mail but doesn't consume it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := openConcurrentDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	mailbox := resolveMailbox(db, *as)
	deadline := time.Now().Add(time.Duration(*timeoutSec) * time.Second)
	for {
		m, err := store.PopOldestUnread(db, mailbox, *keepUnread)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if m != nil {
			if !*keepUnread && mailbox.TaskSlug != "" {
				_ = store.ResetNudges(db, mailbox.TaskSlug)
			}
			printPoppedMessage(m, *jsonOut)
			return 0
		}
		if !*wait || !time.Now().Before(deadline) {
			if !*jsonOut {
				fmt.Println("(no messages)")
			}
			return 0
		}
		time.Sleep(inboxPollInterval)
	}
}

// cmdInboxRead implements `focusflow inbox read <id> [--json]` — shows
// and acks one specific message out of order. the dashboard calls this
// after it has already rendered the message locally and tolerates a
// failure (id already gone), so a missing id is a plain error exit, not
// a crash.
func cmdInboxRead(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println("focusflow inbox read <id> [--json]")
		return 0
	}
	if len(args) == 0 || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: inbox read requires a message id")
		return 2
	}
	id := args[0]
	fs := flagSet("inbox read")
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := openConcurrentDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	m, err := store.GetBusMessage(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: no such message %q\n", id)
		return 1
	}
	if _, err := store.MarkRead(db, id); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	m.Mail = "read"
	printPoppedMessage(m, *jsonOut)
	return 0
}

// ---------- output ----------

type jsonMessageFrom struct {
	TaskSlug *string `json:"task_slug"`
}

type jsonMessage struct {
	ID        string          `json:"id"`
	Body      string          `json:"body"`
	CreatedAt string          `json:"created_at"`
	From      jsonMessageFrom `json:"from"`
	ReplyTo   *string         `json:"reply_to"`
	Urgent    bool            `json:"urgent"`
	Mail      string          `json:"mail"`
	// DeliveredAt is when the recipient session first saw the message.
	DeliveredAt *string `json:"delivered_at,omitempty"`
}

// toJSONMessage shapes a BusMessage the way the dashboard's parseRow
// expects: id, body, created_at, from.task_slug (nullable), reply_to
// (nullable), urgent, mail.
func toJSONMessage(m *store.BusMessage) jsonMessage {
	var from jsonMessageFrom
	if m.FromTaskSlug.Valid && m.FromTaskSlug.String != "" {
		s := m.FromTaskSlug.String
		from.TaskSlug = &s
	}
	var replyTo *string
	if m.ReplyTo.Valid {
		s := m.ReplyTo.String
		replyTo = &s
	}
	var delivered *string
	if m.DeliveredAt.Valid {
		d := m.DeliveredAt.String
		delivered = &d
	}
	return jsonMessage{
		DeliveredAt: delivered,
		ID:          m.ID,
		Body:        m.Body,
		CreatedAt:   m.CreatedAt,
		From:        from,
		ReplyTo:     replyTo,
		Urgent:      m.Urgent,
		Mail:        m.Mail,
	}
}

func printMessagesJSON(msgs []*store.BusMessage) {
	out := make([]jsonMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toJSONMessage(m))
	}
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: marshal messages: %v\n", err)
		return
	}
	fmt.Println(string(b))
}

// printPoppedMessage prints exactly one line of JSON (so a caller reading
// stdout line-by-line — e.g. a Monitor-style event loop — sees exactly
// one event) or a short human-readable block.
func printPoppedMessage(m *store.BusMessage, jsonOut bool) {
	if jsonOut {
		b, err := json.Marshal(toJSONMessage(m))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: marshal message: %v\n", err)
			return
		}
		fmt.Println(string(b))
		return
	}
	from := "user"
	if m.FromTaskSlug.Valid && m.FromTaskSlug.String != "" {
		from = m.FromTaskSlug.String
	}
	urgent := ""
	if m.Urgent {
		urgent = "  [urgent]"
	}
	fmt.Printf("[%s] %s  from:%s%s\n%s\n", m.ID, m.CreatedAt, from, urgent, m.Body)
}

func printMessagesTable(msgs []*store.BusMessage) {
	if len(msgs) == 0 {
		fmt.Println("(no messages)")
		return
	}
	for _, m := range msgs {
		from := "user"
		if m.FromTaskSlug.Valid && m.FromTaskSlug.String != "" {
			from = m.FromTaskSlug.String
		}
		marker := " "
		if m.Mail == "unread" {
			marker = "*"
		}
		urgent := ""
		if m.Urgent {
			urgent = "  [urgent]"
		}
		fmt.Printf("%s [%s] %s  from:%s%s\n    %s\n", marker, m.ID, m.CreatedAt, from, urgent, m.Body)
	}
}

// cmdInboxStats implements `focusflow inbox stats` — the human queue's
// answered/pending counts and wait-time metrics.
func cmdInboxStats(args []string) int {
	fs := flagSet("inbox stats")
	as := fs.String("as", "user", "assignee-level mailbox to summarize")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		return 1
	}
	defer db.Close()

	s, err := store.InboxStatsFor(db, *as)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("answered messages : %d\n", s.Answered)
	fmt.Printf("pending messages  : %d\n", s.Pending)
	fmt.Printf("broadcasts on bus : %d\n", s.Broadcasts)
	fmt.Printf("average wait      : %s\n", formatWait(s.Average))
	fmt.Printf("median wait       : %s\n", formatWait(s.Median))
	fmt.Printf("worst wait        : %s\n", formatWait(s.Worst))
	return 0
}

func formatWait(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return d.String()
}
