package stats

import (
	"fmt"
	"launchbot/utils"
	"time"

	"github.com/dustin/go-humanize"
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
	Notifications       int64
	Commands            int64
	ApiRequests         int64
	RequestsRatelimited int64
	LastApiUpdate       time.Time
	NextApiUpdate       time.Time
	StartedAt           time.Time
}

type ScheduleCmdStats struct {
	CommandCalls        int64
	RefreschCallbacks   int64
	ModeSwitchCallbacks int64
}

type NextCmdStats struct {
	CommandCalls      int64
	RefreshCallbacks  int64
	NextCallbacks     int64
	PreviousCallbacks int64
	ReturnCallbacks   int64
}

type StatsCmdStats struct {
	CommandCalls     int64
	RefreshCallbacks int64
}

// Embedded chat-level statistics
type User struct {
	ReceivedNotifications int64
	SentCommands          int64
	MemberCount           int64
	SubscribedSince       int64
}

func (stats *Statistics) String() string {
	//cpuPerc, err := cpu.Percent(time.Minute, false)
	//if err != nil {
	//	log.Error().Err(err).Msg("Getting CPU usage failed")
	//	cpuPerc = []float64{0.00}
	//}

	// Get subscriber count from disk
	subscribers := 0

	sinceStartup := humanize.Time(stats.StartedAt)

	text := fmt.Sprintf(
		"📊 *LaunchBot global statistics*\n"+
			"Notification delivered: %d\n"+
			"Active subscribers: %d\n"+
			"Commands parsed: %d\n\n"+

			"🌍 *Backend information*\n"+
			"Bot started %s\n"+
			"LaunchBot %s",

		stats.Notifications,
		subscribers,
		stats.Commands,
		sinceStartup,
		stats.RunningVersion,
	)

	return utils.PrepareInputForMarkdown(text, "text")
}
