package api

import (
	"context"
	"launchbot/config"
	"time"

	"github.com/procyon-projects/chrono"
	"github.com/rs/zerolog/log"
)

// Schedule next API call
// Schedule notifications

/*
Schedules a chrono job for when the notification should be sent, with some
margin for one extra API update before the sending process starts.
*/
func scheduleNotifications() bool {
	log.Info().Msg("Pseudo notification enqueued at api.scheduler")

	// TODO when sending a 5-minute notification, schedule a post-launch check
	return true
}

/* Function that chrono calls when a scheduled API update runs. */
func updateWrapper(session *config.Session) {
	log.Info().Msgf("Running scheduled update...")

	// Check return value of updater
	success := Updater(session)

	if !success {
		// TODO define retry time-limit based on error codes (api/errors.go)
		log.Warn().Msg("Running updater failed: retrying in 60 seconds...")

		// Retry twice
		// TODO use expontential back-off?)
		for i := 1; i <= 3; i++ {
			success = Updater(session)
			if !success {
				log.Warn().Msgf("Re-try number %d failed, trying again in %d seconds", i, 60)
				time.Sleep(time.Second * 60)
			} else {
				log.Info().Msgf("Success after %d retries", i)
				break
			}
		}
	}
}

func Scheduler(session *config.Session) bool {
	/* Get time of the next notification. This will be used to
	determine the time of the next API update, as in how far we
	can push the next update. LL2 has rather strict API limits,
	and we'd rather not waste our calls. */
	nextNotif := session.LaunchCache.NextNotificationTime()
	timeUntilNotif := time.Until(time.Unix(nextNotif.SendTime, 0))

	// Time of next scheduled update
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
	if time.Until(autoUpdateTime) > timeUntilNotif {
		log.Info().Msgf("A notification is coming up before next API update")

		// TODO create and queue notification creation + enquement
		// TODO handle case where two notifications have the same send-time
		return scheduleNotifications()
	} else {
		log.Info().Msg("No notifications coming up: using auto-update")
	}

	// Schedule the auto-update
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

	log.Info().Msgf("Next auto-update successfully scheduled for %s",
		autoUpdateTime.String(),
	)

	return true
}
