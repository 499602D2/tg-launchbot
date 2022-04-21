package api

import (
	"context"
	"fmt"
	"launchbot/config"
	"launchbot/db"
	"launchbot/sendables"
	"strings"
	"time"

	"github.com/procyon-projects/chrono"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// Creates and queues a notification
func Notify(launch *db.Launch, db *db.Database) *sendables.Sendable {
	// Create message, get notification type
	text, notification := launch.NotificationMessage()

	// TODO make launch.NotificationMessage produce sendables for multiple platforms
	// V3.1+

	// Send silently if not a 1-hour or 5-minute notification
	sendSilently := true
	if notification.Type == "1hour" || notification.Type == "5min" {
		sendSilently = false
	}

	// TODO: implement callback handling
	muteBtn := tb.InlineButton{
		Text: "🔇 Mute launch",
		Data: fmt.Sprintf("mute/%s", launch.Id),
	}

	// TODO: implement callback handling
	expandBtn := tb.InlineButton{
		Text: "ℹ️ Expand description",
		Data: fmt.Sprintf("exp/%s", launch.Id),
	}

	// Construct the keeb
	kb := [][]tb.InlineButton{
		{muteBtn}, {expandBtn},
	}

	// Message
	msg := sendables.Message{
		TextContent: &text,
		AddUserTime: true,
		RefTime:     launch.NETUnix,
		SendOptions: tb.SendOptions{
			ParseMode:           "MarkdownV2",
			DisableNotification: sendSilently,
			ReplyMarkup:         &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// TODO Get recipients
	recipients := launch.GetRecipients(db, notification)

	/*
		Some notes on just _how_ fast we can send stuff at Telegram's API

		- link tags []() do _not_ count towards the perceived byte-size of
			the message.
		- new-lines are counted as 5 bytes (!)
			- some other symbols, such as '&' or '"" may also count as 5 B

		https://telegra.ph/So-your-bot-is-rate-limited-01-26
	*/

	/* Set rate-limit based on text length
	TODO count markdown, ignore links (insert link later?)
	- does markdown formatting count? */
	perceivedByteLen := len(text)
	perceivedByteLen += strings.Count(text, "\n") * 4 // Additional 4 B per newline

	rateLimit := 30
	if perceivedByteLen >= 512 {
		// TODO update bot's limiter...?
		log.Warn().Msgf("Large message (%d bytes): lowering send-rate to 6 msg/s", perceivedByteLen)
		rateLimit = rateLimit / 5
	}

	// Create sendable
	sendable := sendables.Sendable{
		Type: "notification", Message: &msg, Recipients: recipients,
		RateLimit: rateLimit,
	}

	/*
		Loop over the sent-flags, and ensure every previous state is flagged.
		This is important for launches that come out of the blue, namely launches
		by e.g. China/Chinese companies, where the exact NET may only appear less
		than 24 hours before lift-off.

		As an example, the first notification we send might be the 1-hour notification.
		In this case, we will need to flag the 12-hour and 24-hour notification types
		as sent, as they are no-longer relevant. This is done below.
	*/

	iterMap := map[string]string{
		"5min":   "1hour",
		"1hour":  "12hour",
		"12hour": "24hour",
	}

	// Toggle the current state as sent, after which all flags will be toggled
	passed := false
	for curr, next := range iterMap {
		if !passed && curr == notification.Type {
			// This notification was sent: set state
			launch.Notifications[curr] = true
			passed = true
		}

		if passed {
			// If flag has been set, and last type is flagged as unsent, update flag
			if launch.Notifications[next] == false {
				log.Debug().Msgf("Set %s to true for launch=%s", next, launch.Id)
				launch.Notifications[next] = true
			}
		}
	}

	// TODO do database update...?

	return &sendable
}

/*
NotificationWrapper is called when scheduled notifications
are prepared for sending.
*/
func notificationWrapper(session *config.Session, launchIds []string, refreshData bool) {
	// TODO compare NETs here after update (this is currently useless)
	refreshData = false
	if refreshData {
		updateSuccess := Updater(session, false)

		if updateSuccess {
			log.Info().Msg("Successfully refreshed data in notificationWrapper")
		} else {
			log.Error().Msg("Update failed in notificationWrapper")
			return
		}
	} else {
		log.Debug().Msg("refreshData=false")
	}

	for i, launchId := range launchIds {
		// Pull launch from the cache
		launch, ok := session.LaunchCache.LaunchMap[launchId]

		if !ok {
			log.Error().Msgf("[notificationWrapper] Launch with id=%s not found in cache", launchId)
			return
		}

		log.Info().Msgf("[%d] Creating sendable for launch with name=%s", i+1, launch.Name)

		sendable := Notify(launch, session.Db)
		session.Telegram.Queue.Enqueue(sendable, session.Telegram, false)
	}
}

/*
Schedules a chrono job for when the notification should be sent, with some
margin for one extra API update before the sending process starts.
*/
func NotificationScheduler(session *config.Session, notifTime *db.Notification) bool {
	// TODO update job queue with tags (e.g. "notification", "api") for removal
	// TODO when sending a 5-minute notification, schedule a post-launch check
	// TODO explore issues caused by using sendTime = time.Now()

	log.Debug().Msgf("Creating scheduled jobs for %d launch(es)", len(notifTime.IDs))

	// Create task
	task, err := session.Scheduler.Schedule(func(ctx context.Context) {
		// Run notification sender
		notificationWrapper(session, notifTime.IDs, true)

		// Schedule next API update with some margin for an API call
		// TODO determine margin
		Scheduler(session)
	}, chrono.WithTime(time.Unix(notifTime.SendTime, 0)))

	if err != nil {
		log.Error().Err(err).Msg("Error creating notification task")
		return false
	}

	// Lock session, add task to list of scheduled tasks
	session.Mutex.Lock()
	session.Tasks = append(session.Tasks, task)
	session.Mutex.Unlock()

	until := time.Until(time.Unix(notifTime.SendTime, 0))
	log.Debug().Msgf("Notifications scheduled for %d launch(es), in %s",
		len(notifTime.IDs), until.String())

	return true
}

/*
Schedules the next API call through Chrono, and delegates calls to
the notification scheduler.
*/
func Scheduler(session *config.Session) bool {
	/* Get time of the next notification. This will be used to
	determine the time of the next API update, as in how far we
	can push it. */
	nextNotif := session.LaunchCache.FindNext()
	timeUntilNotif := time.Until(time.Unix(nextNotif.SendTime, 0))

	// Time of next scheduled API update
	var autoUpdateTime time.Time

	/* Decide next update time based on the notification's type, and based on
	the time until said notification. Do note, that this is only a regular,
	scheduled check. A final check will be performed just before a notification
	is sent, independent of these scheduled checks. */
	switch nextNotif.Type {
	case "24hour":
		// 24-hour window (??? ... 24h)
		if timeUntilNotif.Hours() >= 6 {
			autoUpdateTime = time.Now().Add(time.Hour * 6)
		} else {
			autoUpdateTime = time.Now().Add(time.Hour * 3)
		}
	case "12hour":
		// 12-hour window (24h ... 12h)
		autoUpdateTime = time.Now().Add(time.Hour * 3)
	case "1hour":
		// 1-hour window (12h ... 1h)
		if timeUntilNotif.Hours() >= 4 {
			autoUpdateTime = time.Now().Add(time.Hour * 2)
		} else {
			autoUpdateTime = time.Now().Add(time.Hour)
		}
	case "5min":
		// 5-min window (1h ... 5 min), less than 55 minutes
		autoUpdateTime = time.Now().Add(time.Minute * 15)
	default:
		// Default case, needed for debugging without a working database
		log.Error().Msgf("nextNotif.Type fell through: %#v", nextNotif)
		autoUpdateTime = time.Now().Add(time.Hour * 6)
	}

	/* Compare the scheduled update to the notification send-time, and use
	whichever comes first.

	Basically, if there's a notification coming up before the next API update,
	we don't need to schedule an API update at all, as the notification handler
	will perform that for us.

	This is due to the fact that the notification sender needs to check that the
	data is still up to date, and has not changed from the last update. */
	if (time.Until(autoUpdateTime) > timeUntilNotif) && (timeUntilNotif.Minutes() > -5.0) {
		log.Info().Msgf("A notification (type=%s) is coming up before next API update: scheduling",
			nextNotif.Type,
		)
		return NotificationScheduler(session, nextNotif)
	} else {
		log.Debug().Msgf("autoUpdateTime set to %s (type=%s)", autoUpdateTime.String(), nextNotif.Type)
	}

	// Schedule next auto-update, since no notifications are incoming soon
	task, err := session.Scheduler.Schedule(func(ctx context.Context) {
		updateWrapper(session)
	}, chrono.WithTime(autoUpdateTime))

	if err != nil {
		log.Error().Err(err).Msgf("Error scheduling next update")
		return false
	}

	// Lock session, add task to list of scheduled tasks
	session.Mutex.Lock()
	session.Tasks = append(session.Tasks, task)
	session.Mutex.Unlock()

	log.Info().Msgf("Next auto-update scheduled for %s",
		autoUpdateTime.String(),
	)

	return true
}
