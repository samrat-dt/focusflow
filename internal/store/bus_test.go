package store

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func msg(id, to string, task string, opts ...func(*BusMessage)) *BusMessage {
	m := &BusMessage{ID: id, FromAssignee: "user", ToAssignee: to, Body: "b-" + id, CreatedAt: NowISO()}
	if task != "" {
		m.ToTaskSlug = sql.NullString{String: task, Valid: true}
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func TestBusInsertListPopMarkRead(t *testing.T) {
	db := openTempDB(t)
	insertTask(t, db, "w", "W", "backlog", "medium", "/w", nil)
	for _, m := range []*BusMessage{msg("a", "user", "w"), msg("b", "user", "w"), msg("c", "user", "")} {
		if err := InsertBusMessage(db, m); err != nil {
			t.Fatal(err)
		}
	}

	task, _ := ListBusMessages(db, MailboxFilter{TaskSlug: "w"}, false)
	human, _ := ListBusMessages(db, MailboxFilter{Assignee: "user"}, false)
	if len(task) != 2 || len(human) != 1 || task[0].ID != "a" {
		t.Fatalf("mailboxes: task=%d human=%d", len(task), len(human))
	}

	// Peek leaves it unread; pop consumes oldest first.
	if m, _ := PopOldestUnread(db, MailboxFilter{TaskSlug: "w"}, true); m == nil || m.ID != "a" || m.Mail != "unread" {
		t.Fatalf("peek = %+v", m)
	}
	if m, _ := PopOldestUnread(db, MailboxFilter{TaskSlug: "w"}, false); m == nil || m.ID != "a" || m.Mail != "read" || !m.ReadAt.Valid {
		t.Fatalf("pop = %+v", m)
	}
	if m, _ := PopOldestUnread(db, MailboxFilter{TaskSlug: "w"}, false); m == nil || m.ID != "b" {
		t.Fatalf("second pop = %+v", m)
	}
	if m, err := PopOldestUnread(db, MailboxFilter{TaskSlug: "w"}, false); m != nil || err != nil {
		t.Fatalf("empty mailbox pop = %+v, %v", m, err)
	}
	all, _ := ListBusMessages(db, MailboxFilter{TaskSlug: "w"}, true)
	if len(all) != 2 {
		t.Errorf("--all lists read messages too: %d", len(all))
	}

	if found, _ := MarkRead(db, "c"); !found {
		t.Error("MarkRead existing")
	}
	if found, _ := MarkRead(db, "c"); !found {
		t.Error("MarkRead must be idempotent for an already-read message")
	}
	if found, _ := MarkRead(db, "ghost"); found {
		t.Error("MarkRead of a missing id must report not found")
	}
}

func TestWatchesAndFanOutLookups(t *testing.T) {
	db := openTempDB(t)
	for _, w := range [][2]string{{"alice", "t1"}, {"alice", "t1"}, {"bob", "t1"}, {"bob", "proj"}, {"carol", "other"}} {
		if err := Watch(db, w[0], w[1]); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := ListWatches(db, "bob"); !reflect.DeepEqual(got, []string{"proj", "t1"}) {
		t.Errorf("ListWatches(bob) = %v", got)
	}
	if got, _ := WatchersOf(db, "t1", "proj", "", "t1"); !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Errorf("WatchersOf = %v (distinct, sorted)", got)
	}
	if ok, _ := Unwatch(db, "alice", "t1"); !ok {
		t.Error("Unwatch existing")
	}
	if ok, _ := Unwatch(db, "alice", "t1"); ok {
		t.Error("Unwatch missing must report false")
	}
}

func TestInboxStatsFor(t *testing.T) {
	db := openTempDB(t)
	now := time.Now().UTC()
	add := func(id string, broadcast bool, mail string, waited time.Duration) {
		created := now.Add(-time.Hour).Format(time.RFC3339)
		var read any
		if mail == "read" {
			read = now.Add(-time.Hour + waited).Format(time.RFC3339)
		}
		b := 0
		if broadcast {
			b = 1
		}
		if _, err := db.Exec(`INSERT INTO bus_messages (id, from_assignee, to_assignee, body, broadcast, mail, created_at, read_at) VALUES (?, 'user', 'user', 'x', ?, ?, ?, ?)`,
			id, b, mail, created, read); err != nil {
			t.Fatal(err)
		}
	}
	add("1", false, "read", 10*time.Second)
	add("2", false, "read", 20*time.Second)
	add("3", false, "read", 90*time.Second)
	add("4", false, "unread", 0)
	add("5", true, "unread", 0)

	s, err := InboxStatsFor(db, "user")
	if err != nil {
		t.Fatal(err)
	}
	if s.Answered != 3 || s.Pending != 1 || s.Broadcasts != 1 || s.Median != 20*time.Second || s.Worst != 90*time.Second || s.Average != 40*time.Second {
		t.Errorf("stats = %+v", s)
	}
	empty, _ := InboxStatsFor(openTempDB(t), "user")
	if empty.Answered != 0 || empty.Median != 0 {
		t.Errorf("empty stats = %+v", empty)
	}
}

func TestNudgeAndSurfacedBookkeeping(t *testing.T) {
	db := openTempDB(t)
	insertTask(t, db, "w", "W", "backlog", "medium", "/w", nil)
	if st, _ := GetNudgeState(db, "w"); st.Attempts != 0 {
		t.Fatalf("fresh state = %+v", st)
	}
	RecordNudge(db, "w")
	RecordNudge(db, "w")
	if st, _ := GetNudgeState(db, "w"); st.Attempts != 2 || st.NudgedAt == "" {
		t.Errorf("after two nudges = %+v", st)
	}
	ResetNudges(db, "w")
	if st, _ := GetNudgeState(db, "w"); st.Attempts != 0 {
		t.Errorf("reset = %+v", st)
	}

	for i := 1; i <= 3; i++ {
		InsertBusMessage(db, msg(fmt.Sprintf("bc%d", i), "user", "w", func(m *BusMessage) { m.Broadcast = true }))
	}
	InsertBusMessage(db, msg("plain", "user", "w")) // not a broadcast
	p, _ := PendingUnsurfacedForTask(db, "w")
	if len(p) != 3 {
		t.Fatalf("pending = %d, want 3 broadcasts only", len(p))
	}
	MarkSurfaced(db, "w", "bc1", "bc2", "bc1")
	if p, _ = PendingUnsurfacedForTask(db, "w"); len(p) != 1 || p[0].ID != "bc3" {
		t.Errorf("after surfacing = %+v", p)
	}
	MarkRead(db, "bc3")
	if p, _ = PendingUnsurfacedForTask(db, "w"); len(p) != 0 {
		t.Errorf("read broadcasts are not pending: %+v", p)
	}
}

func TestOwnersCRUD(t *testing.T) {
	db := openTempDB(t)
	insertProject(t, db, "p", "P", "/p", "medium")
	o := &Owner{Slug: "o", Name: "O", WorkDir: "/w", ProjectSlug: sql.NullString{String: "p", Valid: true}, Every: "1h"}
	if err := InsertOwner(db, o); err != nil {
		t.Fatal(err)
	}
	if err := InsertOwner(db, o); err == nil {
		t.Error("duplicate slug must fail")
	}
	got, err := GetOwner(db, "o")
	if err != nil || got.Status != "paused" || got.ProjectSlug.String != "p" || got.NextWakeAt.Valid {
		t.Fatalf("owner = %+v, %v", got, err)
	}
	SetOwnerStatus(db, "o", "active", "2030-01-01T00:00:00Z")
	SetOwnerNextWake(db, "o", "2031-01-01T00:00:00Z")
	RecordOwnerTick(db, "o", "running", "2032-01-01T00:00:00Z")
	got, _ = GetOwner(db, "o")
	if got.Status != "active" || got.NextWakeAt.String != "2032-01-01T00:00:00Z" || got.LastTickStatus.String != "running" || !got.LastTickAt.Valid {
		t.Errorf("after updates = %+v", got)
	}
	SetOwnerStatus(db, "o", "retired", "")
	if list, _ := ListOwners(db, false); len(list) != 0 {
		t.Error("retired owners are archived and hidden by default")
	}
	if list, _ := ListOwners(db, true); len(list) != 1 {
		t.Error("include-archived lists retired owners")
	}
	if err := DeleteOwner(db, "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetOwner(db, "o"); err == nil {
		t.Error("deleted owner still readable")
	}
}

func TestListWorkdirs(t *testing.T) {
	db := openTempDB(t)
	UpsertWorkdir(db, "/b", "bee", "", "")
	UpsertWorkdir(db, "/a", "", "", "git@x:a.git")
	got, err := ListWorkdirs(db)
	if err != nil || len(got) != 2 {
		t.Fatalf("workdirs = %+v, %v", got, err)
	}
}

func TestErrSchemaTooNewMessage(t *testing.T) {
	msg := ErrSchemaTooNew{Have: 9, Max: 1}.Error()
	if msg == "" || !strings.Contains(msg, "v9") || !strings.Contains(msg, "make install") {
		t.Errorf("message = %q", msg)
	}
}
