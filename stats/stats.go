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
	LimitsAverage  int64 // Track average enforced limit duration
	LimitsEnforced int64 // Track count of enforced limits
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
	nextUpdate := durafmt.Parse(time.Until(stats.NextApiUpdate)).LimitFirstN(2)
	sinceStartup := humanize.Time(stats.StartedAt)

	// Db size; humanize it
	dbSize := humanize.Bytes(uint64(stats.DbSize))
	rateLimitSI := humanize.SIWithDigits(float64(stats.LimitsAverage)*10e-9, 0, "s")

	text := fmt.Sprintf(
		"📊 *LaunchBot global statistics*\n"+
			"Notifications delivered: %d\n"+
			"Commands parsed: %d\n"+
			"Active subscribers: %d\n\n"+

			"💾 *Database information*\n"+
			"Updated: %s ago\n"+
			"Next update in: %s\n"+
			"Storage used: %s\n\n"+

			"🌍 *Server information*\n"+
			"Bot started %s\n"+
			"Average rate-limit %s\n"+
			"GITHUBLINK",

		stats.Notifications, stats.Commands+stats.Callbacks, subscribers,
		dbLastUpdated, nextUpdate, dbSize, sinceStartup, rateLimitSI,
	)

	text = utils.PrepareInputForMarkdown(text, "text")

	// Set Github link
	link := utils.PrepareInputForMarkdown("https://github.com/499602D2/tg-launchbot", "link")
	linkText := utils.PrepareInputForMarkdown(stats.RunningVersion, "text")
	text = strings.ReplaceAll(text, "GITHUBLINK", fmt.Sprintf("[*LaunchBot %s*](%s)", linkText, link))

	return text
}

func (stats *Statistics) Update(isCommand bool) {
	if isCommand {
		stats.Commands++
	} else {
		stats.Callbacks++
	}
}

func (userStats *User) Update(isCommand bool) {
	if isCommand {
		userStats.SentCommands++
	} else {
		userStats.SentCallbacks++
	}
}
