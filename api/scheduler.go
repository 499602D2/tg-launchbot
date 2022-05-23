package api

import (
	"context"
	"fmt"
	"launchbot/config"
	"launchbot/db"
	"launchbot/sendables"
	"time"

	"github.com/hako/durafmt"
	"github.com/procyon-projects/chrono"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

type PostLaunch struct {
	Do       bool
	NET      int64
	LaunchId string
}

// Notify creates and queues a notification
func Notify(launch *db.Launch, database *db.Database, username string) *sendables.Sendable {
	// Pull the notification type we are sending (could be e.g. cached)
	notification := launch.NextNotification(database)

	// Text content of the notification
	text := launch.NotificationMessage(notification.Type, false, username)
	kb := launch.TelegramNotificationKeyboard(notification.Type)

	// FUTURE make launch.NotificationMessage produce sendables for multiple platforms
	// V3.1+

	// Message
	msg := sendables.Message{
		TextContent: text, AddUserTime: true, RefTime: launch.NETUnix,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Send silently if not a 1-hour or 5-minute notification
	// switch thisNotif.Type {
	// case "24hour", "12hour":
	// 	msg.SendOptions.DisableNotification = true
	// case "1hour", "5min":
	// 	msg.SendOptions.DisableNotification = false
	// }

	// Get list of recipients
	platform := "tg"
	log.Debug().Msgf("Calling NotificationRecipients from scheduler.Notify()")
	recipients := launch.NotificationRecipients(database, notification.Type, platform)

	// Create sendable
	sendable := sendables.Sendable{
		Type:             "notification",
		NotificationType: notification.Type,
		LaunchId:         launch.Id,
		Message:          &msg,
		Recipients:       recipients,
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

		if !passed && notificationType == notification.Type {
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
	launch.NotificationState.UpdateFlags(launch)

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
func notificationWrapper(session *config.Session, launchIds []string, refreshData bool) bool {
	if refreshData {
		// Run updater
		updateWrapper(session, false)

		// Re-get all notifications
		_, notification := session.Cache.NextScheduledUpdateIn()

		// Re-schedule notifications
		return NotificationScheduler(session, notification, false)
	} else {
		log.Debug().Msg("Not refreshing data before notification scheduling...")
	}

	// If we schedule a 5-min notification, schedule a post-launch API update
	var postLaunchUpdate *PostLaunch

	for i, launchId := range launchIds {
		// Pull launch from the cache
		launch, error := session.Cache.FindLaunchById(launchId)

		if error != nil {
			log.Error().Err(error).Msgf("[notificationWrapper] Launch with id=%s not found in cache", launchId)
			continue
		}

		log.Info().Msgf("[%d] Creating sendable for launch with name=%s", i+1, launch.Name)

		// Create the sendable for this notification
		sendable := Notify(launch, session.Db, session.Telegram.Username)

		if sendable.NotificationType == "5min" {
			// If we're sending a 5-min notification, schedule a post-launch update
			log.Debug().Msgf("Sendable has NotificationType==5min, setting queuePostLaunchUpdate=true")
			postLaunchUpdate = &PostLaunch{NET: launch.NETUnix, LaunchId: launch.Id}
		}

		// Enqueue the sendable
		session.Telegram.Queue.Enqueue(sendable, false)
	}

	log.Debug().Msgf("notificationWrapper exiting normally: running scheduler...")

	// Notifications processed: queue post-launch check if this is a 5-minute notification
	Scheduler(session, false, postLaunchUpdate)

	return true
}

// Schedules a chrono job for when the notification should be sent, with some
// margin for one extra API update before the sending process starts.
func NotificationScheduler(session *config.Session, notifTime *db.Notification, refresh bool) bool {
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
		success := notificationWrapper(session, notifTime.IDs, refresh)
		log.Debug().Msgf("NotificationScheduler task: notificationWrapper returned %v", success)
	}, chrono.WithTime(scheduledTime))

	if err != nil {
		log.Error().Err(err).Msg("Error creating notification task!")
		return false
	}

	// Lock session, add task to list of scheduled tasks
	session.Mutex.Lock()

	session.NotificationTasks[scheduledTime] = &task

	session.Mutex.Unlock()

	log.Info().Msgf("Notifications scheduled for %d launch(es), in %s",
		len(notifTime.IDs), durafmt.Parse(time.Until(scheduledTime)).LimitFirstN(2))

	return true
}

// Schedules the next API call through Chrono, and delegates calls to
// the notification scheduler.
func Scheduler(session *config.Session, startup bool, postLaunchCheck *PostLaunch) bool {
	// Get interval until next API update and the next upcoming notification
	untilNextUpdate, notification := session.Cache.NextScheduledUpdateIn()

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
	untilNotification := time.Until(time.Unix(notification.SendTime, 0))

	if postLaunchCheck != nil {
		// If a post-launch check, try scheduling for 10 minutes after launch's NET
		if postLaunchCheck.NET != 0 {
			autoUpdateTime = time.Unix(postLaunchCheck.NET, 0).Add(time.Duration(10) * time.Minute)
			log.Debug().Msgf("postLaunchCheck's NET != 0, scheduling for 10 minutes after NET=%d", postLaunchCheck.NET)
		} else {
			// If notifiation has LaunchNET set to zero, schedule for 15 minutes from now
			autoUpdateTime = time.Now().Add(time.Duration(15) * time.Minute)
			log.Warn().Msgf("postLaunchCheck has NET set to zero, postLaunchCheck=%#v", postLaunchCheck)
		}

		log.Debug().Msgf("postLaunchCheck==true, set autoUpdateTime to %s",
			durafmt.Parse(time.Until(autoUpdateTime)).LimitFirstN(2))
	}

	/*
		Compare the scheduled update to the notification send-time, and use
		whichever comes first.

		Basically, if there's a notification coming up before the next API update,
		we don't need to schedule an API update at all, as the notification handler
		will perform that for us.

		This is due to the fact that the notification sender needs to check that the
		data is still up to date, and has not changed from the last update.
	*/
	if (time.Until(autoUpdateTime) > untilNotification) && (untilNotification.Minutes() > -5.0) {
		log.Info().Msgf("A notification (type=%s) is coming up before next API update, scheduling...",
			notification.Type,
		)

		// Save stats
		session.Telegram.Stats.NextApiUpdate = time.Now().Add(untilNotification)
		return NotificationScheduler(session, notification, true)
	}

	// Schedule next auto-update, since no notifications are incoming soon
	task, err := session.Scheduler.Schedule(func(ctx context.Context) {
		updateWrapper(session, true)
	}, chrono.WithTime(autoUpdateTime))

	if err != nil {
		log.Error().Err(err).Msgf("Scheduling next update failed")
		return false
	}

	// Lock session, add task to list of scheduled tasks
	session.Mutex.Lock()
	session.Tasks = append(session.Tasks, &task)
	session.Mutex.Unlock()

	log.Info().Msgf("Next auto-update in %s (%s)",
		durafmt.Parse(time.Until(autoUpdateTime)).LimitFirstN(2),
		autoUpdateTime.Format(time.RFC1123))

	// Save stats
	session.Telegram.Stats.NextApiUpdate = autoUpdateTime

	// Clean user cache: no notifications coming up, so it should be safe
	if (time.Until(autoUpdateTime) > time.Duration(1)*time.Hour) && !startup {
		log.Debug().Msg("More than an hour until next update, cleaning user-cache")
		session.Cache.CleanUserCache(session.Db, false)
		log.Debug().Msg("User-cache cleaned")
	}

	return true
}
