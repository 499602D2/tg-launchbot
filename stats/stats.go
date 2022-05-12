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
	RunningVersion      string `gorm:"-:all"`
	Platform            string `gorm:"primaryKey;uniqueIndex"`
	Notifications       int
	Commands            int
	ApiRequests         int
	RequestsRatelimited int
	LastApiUpdate       time.Time
	NextApiUpdate       time.Time
	StartedAt           time.Time
}

type ScheduleCmdStats struct {
	CommandCalls        int
	RefreschCallbacks   int
	ModeSwitchCallbacks int
}

type NextCmdStats struct {
	CommandCalls      int
	RefreshCallbacks  int
	NextCallbacks     int
	PreviousCallbacks int
	ReturnCallbacks   int
}

type StatsCmdStats struct {
	CommandCalls     int
	RefreshCallbacks int
}

// Embedded chat-level statistics
type User struct {
	ReceivedNotifications int
	SentCommands          int
	MemberCount           int `gorm:"default:2"`
	SubscribedSince       int64
}

func (stats *Statistics) String(subscribers int) string {
	// Time-related stats
	dbLastUpdated := durafmt.Parse(time.Since(stats.LastApiUpdate)).LimitFirstN(2)
	dbNextUpdate := durafmt.Parse(time.Until(stats.NextApiUpdate)).LimitFirstN(1)
	sinceStartup := humanize.Time(stats.StartedAt)

	text := fmt.Sprintf(
		"📊 *LaunchBot global statistics*\n"+
			"Notification delivered: %d\n"+
			"Active subscribers: %d\n"+
			"Commands parsed: %d\n\n"+

			"💾 *Database information*\n"+
			"Updated %s ago\n"+
			"Next update in %s\n\n"+

			"🌍 *Server information*\n"+
			"Bot started %s\n"+
			"GITHUBLINK",

		stats.Notifications, subscribers, stats.Commands,
		dbLastUpdated, dbNextUpdate, sinceStartup,
	)

	text = utils.PrepareInputForMarkdown(text, "text")

	// Set the link
	link := utils.PrepareInputForMarkdown("https://github.com/499602D2/tg-launchbot", "link")
	linkText := utils.PrepareInputForMarkdown(stats.RunningVersion, "text")
	text = strings.ReplaceAll(text, "GITHUBLINK", fmt.Sprintf("[*LaunchBot %s*](%s)", linkText, link))

	return text
}
