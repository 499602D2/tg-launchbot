package stats

import "time"

/* TODO
- add a "next API update in..." field
- add a next notification type, and the launch it's for
- add a trailing-day set of stats (commands, notifications, callbacks)
- add ratelimits enforced statistic (if limiter returns false)
*/

// Global statistics, per-platform
type Statistics struct {
	Platform      string `gorm:"primaryKey;uniqueIndex"`
	Notifications int64
	Commands      int64
	ApiRequests   int64
	LastApiUpdate time.Time
	UpdatedAt     time.Time
}

// Embedded chat-level statistics
type User struct {
	ReceivedNotifications int64
	SentCommands          int64
	MemberCount           int64
	SubscribedSince       int64
}
