package stats

import (
	"fmt"
	"launchbot/utils"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/hako/durafmt"
)

/* TODO
- add a "next API update in..." field
- add a next notification type, and the launch it's for
- add a trailing-day set of stats (commands, notifications, callbacks)
- add ratelimits enforced statistic (if limiter returns false)
*/

// Global statistics, per-platform
type Statistics struct {
	Platform       string `gorm:"primaryKey;uniqueIndex"`
	Notifications  int
	Commands       int
	Callbacks      int
	ApiRequests    int
	LastApiUpdate  time.Time
	NextApiUpdate  time.Time
	StartedAt      time.Time
	DbSize         int64  `gorm:"-:all"`
	RunningVersion string `gorm:"-:all"`
}

// Embedded chat-level statistics
type User struct {
	ReceivedNotifications int
	SentCommands          int
	SentCallbacks         int
	MemberCount           int `gorm:"default:1"`
	SubscribedSince       int64
}

func (stats *Statistics) String(subscribers int) string {
	// Time-related stats
	dbLastUpdated := durafmt.Parse(time.Since(stats.LastApiUpdate)).LimitFirstN(2)
	// dbNextUpdate := durafmt.Parse(time.Until(stats.NextApiUpdate)).LimitFirstN(2)
	sinceStartup := humanize.Time(stats.StartedAt)

	// Db size
	dbSize := humanize.Bytes(uint64(stats.DbSize))

	text := fmt.Sprintf(
		"📊 *LaunchBot global statistics*\n"+
			"Notification delivered: %d\n"+
			"Commands parsed: %d\n"+
			"Active subscribers: %d\n\n"+

			"💾 *Database information*\n"+
			"Updated: %s ago\n"+
			"Storage used: %s\n\n"+

			"🌍 *Server information*\n"+
			"Bot started %s\n"+
			"GITHUBLINK",

		stats.Notifications, stats.Commands+stats.Callbacks, subscribers,
		dbLastUpdated, dbSize, sinceStartup,
	)

	text = utils.PrepareInputForMarkdown(text, "text")

	// Set Github link
	link := utils.PrepareInputForMarkdown("https://github.com/499602D2/tg-launchbot", "link")
	linkText := utils.PrepareInputForMarkdown(stats.RunningVersion, "text")
	text = strings.ReplaceAll(text, "GITHUBLINK", fmt.Sprintf("[*LaunchBot %s*](%s)", linkText, link))

	return text
}
