package api

import (
	"context"
	"fmt"
	"launchbot/config"
	"launchbot/db"
	"launchbot/sendables"
	"strings"
	"time"

	"github.com/hako/durafmt"
	"github.com/procyon-projects/chrono"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// Notify creates and queues a notification
func Notify(launch *db.Launch, database *db.Database) *sendables.Sendable {
	// Create message, get notification type
	thisNotif := launch.NextNotification(database)
	text := launch.NotificationMessage(thisNotif.Type, false)

	// TODO make launch.NotificationMessage produce sendables for multiple platforms
	// V3.1+

	// TODO: implement callback handling
	muteBtn := tb.InlineButton{
		Text: "🔇 Mute launch",
		Data: fmt.Sprintf("mute/%s", launch.Id),
	}

	// TODO: implement callback handling
	expandBtn := tb.InlineButton{
		Text: "ℹ️ Expand description",
		Data: fmt.Sprintf("exp/%s/%s", launch.Id, thisNotif.Type),
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
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Send silently if not a 1-hour or 5-minute notification
	switch thisNotif.Type {
	case "24hour", "12hour":
		msg.SendOptions.DisableNotification = true
	case "1hour", "5min":
		msg.SendOptions.DisableNotification = false
	}

	// Get list of recipients
	recipients := launch.GetRecipients(database, &thisNotif)

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
	times := []string{"5min", "1h", "12h", "24h"}
	iterMap := map[string]string{
		"5min": "1h",
		"1h":   "12h",
		"12h":  "24h",
	}

	// Toggle the current state as sent, after which all flags will be toggled.
	passed := false
	for _, notificationType := range times {
		// Get the notification type that should have been sent before this one
		previousType, ok := iterMap[notificationType]

		if !passed && notificationType == thisNotif.Type {
			// This notification was sent: set state, sprintf to correct format.
			launch.NotificationState.Map[fmt.Sprintf("Sent%s", notificationType)] = true
			passed = true
		}

		if passed && ok {
			// If flag has been set, and last type is flagged as unsent, update flag
			if launch.NotificationState.Map[fmt.Sprintf("Sent%s", previousType)] == false {
				log.Debug().Msgf("Set %s to true for launch=%s", previousType, launch.Id)
				launch.NotificationState.Map[fmt.Sprintf("Sent%s", previousType)] = true
			}
		}
	}

	// Flags in the notification-state map have been set: automatically update the
	// boolean values that are thrown into the database.
	launch.NotificationState = launch.NotificationState.UpdateFlags()

	// Push changes to database
	err := database.Update([]*db.Launch{launch}, false, true)

	if err != nil {
		log.Error().Err(err).Msg("Dumping updated launch notification states to disk failed")
	} else {
		log.Debug().Msg("Launch with updated notification states dumped to database")
	}

	return &sendable
}

// NotificationWrapper is called when scheduled notifications are prepared for sending.
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

// Schedules a chrono job for when the notification should be sent, with some
// margin for one extra API update before the sending process starts.
func NotificationScheduler(session *config.Session, notifTime *db.Notification) bool {
	// TODO update job queue with tags (e.g. "notification", "api") for removal
	// TODO when sending a 5-minute notification, schedule a post-launch check
	// TODO explore issues caused by using sendTime = time.Now()

	log.Debug().Msgf("Creating scheduled jobs for %d launch(es)", len(notifTime.IDs))

	// How many seconds before notification time to start the send process
	scheduledTime := time.Unix(notifTime.SendTime, 0)

	// Check if a task has been scheduled for this time instance
	scheduled, ok := session.NotificationTasks[scheduledTime]
	if ok {
		log.Warn().Msgf("Found already scheduled task for this send-time: %+v", scheduled)
	}

	// Create task
	task, err := session.Scheduler.Schedule(func(ctx context.Context) {
		// Run notification sender
		notificationWrapper(session, notifTime.IDs, true)

		// Schedule next API update with some margin for an API call
		Scheduler(session, false)
	}, chrono.WithTime(scheduledTime))

	if err != nil {
		log.Error().Err(err).Msg("Error creating notification task")
		return false
	}

	// Lock session, add task to list of scheduled tasks
	session.Mutex.Lock()

	// TODO keep tasks in database, reload on startup (?)
	session.NotificationTasks[scheduledTime] = &task

	session.Mutex.Unlock()

	until := time.Until(scheduledTime)
	log.Debug().Msgf("Notifications scheduled for %d launch(es), in %s",
		len(notifTime.IDs), until.String())

	return true
}

// Schedules the next API call through Chrono, and delegates calls to
// the notification scheduler.
func Scheduler(session *config.Session, startup bool) bool {
	// Get interval until next API update and the next upcoming notification
	untilNextUpdate, notification := session.LaunchCache.NextScheduledUpdateIn()

	if startup {
		// On startup, check if database needs an immediate update
		updateNow, sinceLast := session.Db.RequiresImmediateUpdate(untilNextUpdate)
		session.Telegram.Stats.LastApiUpdate = time.Now().Add(-sinceLast)

		if updateNow {
			// Database is out of date: update now
			return Updater(session, true)
		}

		// No need to update now, but deduct the time since last update from next update
		untilNextUpdate = untilNextUpdate - sinceLast
	}

	// Time of next scheduled API update and time until next notification
	autoUpdateTime := time.Now().Add(untilNextUpdate)
	untilSendTime := time.Until(time.Unix(notification.SendTime, 0))

	/* Compare the scheduled update to the notification send-time, and use
	whichever comes first.

	Basically, if there's a notification coming up before the next API update,
	we don't need to schedule an API update at all, as the notification handler
	will perform that for us.

	This is due to the fact that the notification sender needs to check that the
	data is still up to date, and has not changed from the last update. */
	if (time.Until(autoUpdateTime) > untilSendTime) && (untilSendTime.Minutes() > -5.0) {
		log.Info().Msgf("A notification (type=%s) is coming up before next API update, scheduling...",
			notification.Type,
		)

		// Save stats
		session.Telegram.Stats.NextApiUpdate = time.Now().Add(untilSendTime)
		return NotificationScheduler(session, notification)
	}

	// Schedule next auto-update, since no notifications are incoming soon
	task, err := session.Scheduler.Schedule(func(ctx context.Context) {
		updateWrapper(session)
	}, chrono.WithTime(autoUpdateTime))

	if err != nil {
		log.Error().Err(err).Msgf("Scheduling next update failed")
		return false
	}

	// Lock session, add task to list of scheduled tasks
	session.Mutex.Lock()
	session.Tasks = append(session.Tasks, &task)
	session.Mutex.Unlock()

	untilAutoUpdate := durafmt.Parse(time.Until(autoUpdateTime)).LimitFirstN(2)
	log.Info().Msgf("Next auto-update in %s (%s)", untilAutoUpdate, autoUpdateTime.Format(time.RFC1123))

	// Save stats
	session.Telegram.Stats.NextApiUpdate = autoUpdateTime

	return true
}
