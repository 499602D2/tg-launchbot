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
- add a next notification type, and the launch it's for
- add a trailing-day set of stats (commands, notifications, callbacks)
*/

// Global statistics, per-platform
type Statistics struct {
	Platform          string `gorm:"primaryKey;uniqueIndex"`
	Notifications     int
	Commands          int
	Callbacks         int
	V2Commands        int // Combined commands + callbacks for pre-v3
	ApiRequests       int
	LimitsAverage     float64 // Track average enforced limit duration
	LimitsEnforced    int64   // Track count of enforced limits
	LastApiUpdate     time.Time
	NextApiUpdate     time.Time
	NextNotification  time.Time
	StartedAt         time.Time
	Subscribers       int64  `gorm:"-:all"`
	WeeklyActiveUsers int64  `gorm:"-:all"`
	DbSize            int64  `gorm:"-:all"`
	RunningVersion    string `gorm:"-:all"`
}

// Embedded chat-level statistics
type User struct {
	ReceivedNotifications int
	SentCommands          int
	SentCallbacks         int
	MemberCount           int `gorm:"default:1"`
	SubscribedSince       int64
}

func (stats *Statistics) String() string {
	var (
		// nextUpdate       string
		nextNotification string
	)

	// Time-related stats
	dbLastUpdated := durafmt.Parse(time.Since(stats.LastApiUpdate)).LimitFirstN(2)

	// if time.Until(stats.NextApiUpdate) <= 0 {
	// 	nextUpdate = "now"
	// } else {
	// 	nextUpdate = "in: " + durafmt.Parse(time.Until(stats.NextApiUpdate)).LimitFirstN(2).String()
	// }

	if time.Until(stats.NextNotification) <= 0 {
		nextNotification = "being sent..."
	} else if stats.NextNotification.Unix() == 0 {
		nextNotification = "status unknown"
	} else {
		nextNotification = "in " + durafmt.Parse(time.Until(stats.NextNotification)).LimitFirstN(2).String()
	}

	text := fmt.Sprintf(
		"📊 *LaunchBot global statistics*\n"+
			"Notifications delivered: %s\n"+
			"Commands parsed: %s\n"+
			"Active subscribers: %s\n"+
			"Weekly active users: %s\n\n"+

			"🛰️ *Database information*\n"+
			"Updated %s ago\n"+
			"Notification %s\n"+
			"Storage used: %s\n\n"+

			"🌍 *Server information*\n"+
			"Bot started %s ago\n"+
			"Average rate-limit %s\n"+
			"GITHUBLINK",

		// General statistics
		humanize.Comma(int64(stats.Notifications)),
		humanize.Comma(int64(stats.Commands+stats.Callbacks+stats.V2Commands)),
		humanize.Comma(stats.Subscribers), humanize.Comma(stats.WeeklyActiveUsers),

		// API update information
		dbLastUpdated, nextNotification, humanize.Bytes(uint64(stats.DbSize)),

		// Server information
		durafmt.Parse(time.Since(stats.StartedAt)).LimitFirstN(2).String(),
		humanize.SIWithDigits(stats.LimitsAverage, 1, "s"),
	)

	text = utils.PrepareInputForMarkdown(text, "text")

	// Set Github link
	link := utils.PrepareInputForMarkdown("https://github.com/499602D2/tg-launchbot", "link")
	linkText := utils.PrepareInputForMarkdown(stats.RunningVersion, "text")
	text = strings.ReplaceAll(text, "GITHUBLINK", fmt.Sprintf("[*LaunchBot %s*](%s)", linkText, link))

	return text
}

// Update global statistics
func (stats *Statistics) Update(isCommand bool) {
	if isCommand {
		stats.Commands++
	} else {
		stats.Callbacks++
	}
}

// Update user-specific statistics
func (userStats *User) Update(isCommand bool) {
	if isCommand {
		userStats.SentCommands++
	} else {
		userStats.SentCallbacks++
	}
}
