package app

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"focusflow/internal/flowdb"
	"os"
	"strings"
)

// newMessageID returns a new 8-hex-char message id, e.g. "07cfae37".
// Overridable in tests for deterministic output. Matches the shape
// control-center's parseFlowId regex expects: /\[([0-9a-f]{8})\]/.
var newMessageID = func() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand read: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// resolveCurrentTaskSlug returns the task bound to the active harness
// session, or "" if this process isn't running inside one (a dispatch
// session, control-center, or a bare terminal). Mirrors the flow skill's
// §10 rule: session_id is the single source of truth, found the same way
// currentHarnessSessionID/doHere do. Falls back to $FOCUSFLOW_TASK for
// contexts where the env var is set but the session_id lookup can't run
// (e.g. a `do --auto` child process's own sub-invocations).
func resolveCurrentTaskSlug(db *sql.DB) string {
	if sid := currentHarnessSessionID(); sid != "" {
		var slug string
		err := db.QueryRow(`SELECT slug FROM tasks WHERE session_id = ? AND archived_at IS NULL`, sid).Scan(&slug)
		if err == nil {
			return slug
		}
	}
	return os.Getenv("FOCUSFLOW_TASK")
}

// messageAddress is a parsed "<assignee>[/<task-slug>]" bus address.
type messageAddress struct {
	Assignee string
	TaskSlug string // "" for an assignee-level mailbox like "user"
}

func (a messageAddress) String() string {
	if a.TaskSlug == "" {
		return a.Assignee
	}
	return a.Assignee + "/" + a.TaskSlug
}

// parseMessageAddress splits "<assignee>[/<task-slug>]" into its parts.
func parseMessageAddress(raw string) (messageAddress, error) {
	if raw == "" {
		return messageAddress{}, fmt.Errorf("empty address")
	}
	parts := strings.SplitN(raw, "/", 2)
	assignee := parts[0]
	if assignee == "" {
		return messageAddress{}, fmt.Errorf("address %q has an empty assignee", raw)
	}
	if len(parts) == 1 {
		return messageAddress{Assignee: assignee}, nil
	}
	taskSlug := parts[1]
	if taskSlug == "" || strings.Contains(taskSlug, "/") {
		return messageAddress{}, fmt.Errorf("address %q must be <assignee> or <assignee>/<task-slug>", raw)
	}
	return messageAddress{Assignee: assignee, TaskSlug: taskSlug}, nil
}

// messageArgs holds the parsed result of `focusflow message`'s argument
// list. Real flow's contract is "flags may come in any order; the body is
// positional or --body". flag.FlagSet stops at the first non-flag arg, so
// this is a small manual scanner instead — it also means a leading
// "--help" is recognized as a flag (see looksLikeFlag doc comment) rather
// than silently consumed as the address positional.
type messageArgs struct {
	Target  string
	Body    string
	Urgent  bool
	ReplyTo string
}

func parseMessageArgs(args []string) (messageArgs, error) {
	var out messageArgs
	var positionals []string
	var bodyFlagSet bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--urgent":
			out.Urgent = true
		case a == "--reply-to":
			i++
			if i >= len(args) {
				return messageArgs{}, fmt.Errorf("--reply-to requires a value")
			}
			out.ReplyTo = args[i]
		case strings.HasPrefix(a, "--reply-to="):
			out.ReplyTo = strings.TrimPrefix(a, "--reply-to=")
		case a == "--body":
			i++
			if i >= len(args) {
				return messageArgs{}, fmt.Errorf("--body requires a value")
			}
			out.Body = args[i]
			bodyFlagSet = true
		case strings.HasPrefix(a, "--body="):
			out.Body = strings.TrimPrefix(a, "--body=")
			bodyFlagSet = true
		case looksLikeFlag(a):
			return messageArgs{}, fmt.Errorf("unknown flag %q", a)
		default:
			positionals = append(positionals, a)
		}
	}
	if len(positionals) == 0 {
		return messageArgs{}, fmt.Errorf("requires a target address, e.g. \"user\" or \"user/<task-slug>\"")
	}
	out.Target = positionals[0]
	if len(positionals) > 1 {
		if bodyFlagSet {
			return messageArgs{}, fmt.Errorf("body given both positionally and via --body")
		}
		out.Body = positionals[1]
	}
	if len(positionals) > 2 {
		return messageArgs{}, fmt.Errorf("too many positional arguments")
	}
	if strings.TrimSpace(out.Body) == "" {
		return messageArgs{}, fmt.Errorf("message body must not be empty")
	}
	return out, nil
}

// cmdMessage implements `focusflow message <assignee>[/<task-slug>] "<body>" [--urgent] [--reply-to <id>]`.
func cmdMessage(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printMessageUsage()
		return 0
	}
	parsed, err := parseMessageArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	target, err := parseMessageAddress(parsed.Target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	dbPath, err := flowDBPath()
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

	if target.TaskSlug != "" {
		if _, err := ResolveTask(db, target.TaskSlug, false); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	fromTaskSlug := resolveCurrentTaskSlug(db)
	if fromTaskSlug != "" {
		if target.TaskSlug == fromTaskSlug {
			fmt.Fprintln(os.Stderr, "error: cannot message your own address")
			return 2
		}
	} else if target.TaskSlug == "" && target.Assignee == "user" {
		fmt.Fprintln(os.Stderr, "error: cannot message your own address")
		return 2
	}

	if parsed.ReplyTo != "" {
		if _, err := flowdb.GetBusMessage(db, parsed.ReplyTo); err != nil {
			fmt.Fprintf(os.Stderr, "error: --reply-to %q: no such message\n", parsed.ReplyTo)
			return 1
		}
	}

	id, err := newMessageID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	m := &flowdb.BusMessage{
		ID:           id,
		FromAssignee: "user",
		ToAssignee:   target.Assignee,
		Body:         parsed.Body,
		Urgent:       parsed.Urgent,
		CreatedAt:    flowdb.NowISO(),
	}
	if fromTaskSlug != "" {
		m.FromTaskSlug = sql.NullString{String: fromTaskSlug, Valid: true}
	}
	if target.TaskSlug != "" {
		m.ToTaskSlug = sql.NullString{String: target.TaskSlug, Valid: true}
	}
	if parsed.ReplyTo != "" {
		m.ReplyTo = sql.NullString{String: parsed.ReplyTo, Valid: true}
	}
	if err := flowdb.InsertBusMessage(db, m); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Message sent to %s [%s]\n", target.String(), id)
	return 0
}

// cmdBroadcast implements `focusflow broadcast "<one-liner>"`. Watches
// (subscriber lists per task/project/assignee) are out of scope for this
// pass — see the focusflow-messaging task brief — so a broadcast today
// always lands in the human's own mailbox, the one universal recipient
// that exists without a watch registry. Once `focusflow watch` exists,
// this is the fan-out point to extend.
func cmdBroadcast(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(`focusflow broadcast "<one-liner>" [--urgent]`)
		return 0
	}
	var body string
	var urgent bool
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--urgent":
			urgent = true
		case a == "--body":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --body requires a value")
				return 2
			}
			body = args[i]
		case strings.HasPrefix(a, "--body="):
			body = strings.TrimPrefix(a, "--body=")
		case looksLikeFlag(a):
			fmt.Fprintf(os.Stderr, "error: unknown flag %q\n", a)
			return 2
		default:
			positionals = append(positionals, a)
		}
	}
	if body == "" {
		if len(positionals) == 0 {
			fmt.Fprintln(os.Stderr, "error: broadcast requires a one-liner body")
			return 2
		}
		body = positionals[0]
	}
	if strings.TrimSpace(body) == "" {
		fmt.Fprintln(os.Stderr, "error: broadcast body must not be empty")
		return 2
	}

	dbPath, err := flowDBPath()
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

	fromTaskSlug := resolveCurrentTaskSlug(db)
	id, err := newMessageID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	m := &flowdb.BusMessage{
		ID:           id,
		FromAssignee: "user",
		ToAssignee:   "user",
		Body:         body,
		Urgent:       urgent,
		Broadcast:    true,
		CreatedAt:    flowdb.NowISO(),
	}
	if fromTaskSlug != "" {
		m.FromTaskSlug = sql.NullString{String: fromTaskSlug, Valid: true}
	}
	if err := flowdb.InsertBusMessage(db, m); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Broadcast sent [%s]\n", id)
	return 0
}

func printMessageUsage() {
	fmt.Println(`focusflow message <assignee>[/<task-slug>] "<body>" [--urgent] [--reply-to <id>]

Message a human ("user" is the local human) or the session bound to a
task. Never your own address. Alias: msg. Flags may come in any order;
the body is positional or --body "<text>".`)
}
