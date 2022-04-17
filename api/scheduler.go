package api

import (
	"context"
	"launchbot/config"
	"launchbot/ll2"
	"time"

	"github.com/procyon-projects/chrono"
	"github.com/rs/zerolog/log"
)

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
		launch, ok := session.LaunchCache.Launches[launchId]

		if !ok {
			log.Error().Msgf("[notificationWrapper] Launch with id=%s not found in cache", launchId)
			return
		}

		log.Info().Msgf("[%d] Creating sendable for launch with name=%s", i+1, launch.Name)

		sendable := launch.Notify(session.Db)
		session.Telegram.Queue.Enqueue(sendable, session.Telegram, false)
	}
}

/*
Schedules a chrono job for when the notification should be sent, with some
margin for one extra API update before the sending process starts.
*/
func NotificationScheduler(session *config.Session, notifTime *ll2.NotificationTime) bool {
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
