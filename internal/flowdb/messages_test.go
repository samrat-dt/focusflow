package flowdb

import (
	"database/sql"
	"testing"
)

// TestInsertBusMessageIfAbsentInsertsNewRow pins the basic insert path
// for the one-time flow->focusflow message migration: unlike
// InsertBusMessage (which always sets mail='unread' and stamps
// created_at itself), this preserves historical id/created_at/mail/
// read_at exactly as given, since the row already happened in the past.
func TestInsertBusMessageIfAbsentInsertsNewRow(t *testing.T) {
	db := openTempDB(t)
	m := &BusMessage{
		ID:           "msg-1",
		FromAssignee: "user",
		ToAssignee:   "user",
		Body:         "historical message",
		Urgent:       true,
		Broadcast:    false,
		Mail:         "read",
		CreatedAt:    "2026-01-01T00:00:00Z",
		ReadAt:       sql.NullString{String: "2026-01-01T00:00:05Z", Valid: true},
	}

	inserted, err := InsertBusMessageIfAbsent(db, m)
	if err != nil {
		t.Fatalf("InsertBusMessageIfAbsent: %v", err)
	}
	if !inserted {
		t.Fatal("expected inserted=true for a new id")
	}

	got, err := GetBusMessage(db, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "historical message" || !got.Urgent || got.Mail != "read" ||
		got.CreatedAt != "2026-01-01T00:00:00Z" || !got.ReadAt.Valid || got.ReadAt.String != "2026-01-01T00:00:05Z" {
		t.Errorf("mismatch: %+v", got)
	}
}

// TestInsertBusMessageIfAbsentIdempotent pins the re-run safety the
// migration command depends on: inserting the same id twice is a no-op
// the second time, not an error and not a duplicate row.
func TestInsertBusMessageIfAbsentIdempotent(t *testing.T) {
	db := openTempDB(t)
	m := &BusMessage{
		ID: "msg-dup", FromAssignee: "user", ToAssignee: "user", Body: "once",
		Mail: "unread", CreatedAt: "2026-01-01T00:00:00Z",
	}

	if _, err := InsertBusMessageIfAbsent(db, m); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	inserted, err := InsertBusMessageIfAbsent(db, m)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if inserted {
		t.Error("expected inserted=false on the second call for the same id")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bus_messages WHERE id = 'msg-dup'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row, got %d", count)
	}
}

// TestInsertBusMessageIfAbsentPreservesReplyTo pins that a reply chain
// survives the migration: reply_to is written through untouched.
func TestInsertBusMessageIfAbsentPreservesReplyTo(t *testing.T) {
	db := openTempDB(t)
	original := &BusMessage{
		ID: "msg-orig", FromAssignee: "user", ToAssignee: "user", Body: "original",
		Mail: "read", CreatedAt: "2026-01-01T00:00:00Z",
	}
	if _, err := InsertBusMessageIfAbsent(db, original); err != nil {
		t.Fatalf("insert original: %v", err)
	}

	reply := &BusMessage{
		ID: "msg-reply", FromAssignee: "user", ToAssignee: "user", Body: "reply",
		Mail: "read", CreatedAt: "2026-01-01T00:01:00Z",
		ReplyTo: sql.NullString{String: "msg-orig", Valid: true},
	}
	if _, err := InsertBusMessageIfAbsent(db, reply); err != nil {
		t.Fatalf("insert reply: %v", err)
	}

	got, err := GetBusMessage(db, "msg-reply")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReplyTo.Valid || got.ReplyTo.String != "msg-orig" {
		t.Errorf("ReplyTo = %+v, want msg-orig", got.ReplyTo)
	}
}
