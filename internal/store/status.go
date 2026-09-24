package store

// Task stages, in board order. "backlog" is unpicked work, "next" is the
// short literal queue, "paused" is deliberately parked (a terminal may
// still be open — that is a separate marker, not a status), "review" means
// the agent believes the goal is met and it awaits approval.
const (
	StatusBacklog    = "backlog"
	StatusNext       = "next"
	StatusInProgress = "in-progress"
	StatusPaused     = "paused"
	StatusReview     = "review"
	StatusDone       = "done"
)

// Statuses lists every valid task status in board order.
var Statuses = []string{StatusBacklog, StatusNext, StatusInProgress, StatusPaused, StatusReview, StatusDone}

// IsValidStatus reports whether s is a task status.
func IsValidStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

// StatusUsage is the flag help text listing the statuses.
const StatusUsage = "backlog|next|in-progress|paused|review|done"

// statusCheckSQL is the CHECK clause the tasks table carries after schema v2.
const statusCheckSQL = "CHECK (status IN ('backlog','next','in-progress','paused','review','done'))"
