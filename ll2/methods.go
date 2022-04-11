package ll2

import (
	"time"

	"github.com/rs/zerolog/log"
)

type NotificationTime struct {
	Type     string // In (24hour, 12hour, 1hour, 5min)
	SendTime int64  // Unix-time of the notification
	AllSent  bool   // All notifications sent already?
	LaunchId string
}

/* Returns the first unsent notification type for the launch. */
func (launch *Launch) NextNotification() NotificationTime {
	// TODO do this smarter instead of re-declaring a billion times
	NotificationSendTimes := map[string]int64{
		"24hour": int64(time.Hour) * 24,
		"12hour": int64(time.Hour) * 12,
		"1hour":  int64(time.Hour) * 1,
		"5min":   int64(time.Minute) * 5,
	}

	for notifType, status := range launch.Notifications {
		// Map starts from 24hour, goes down to 5min
		if status == false {
			sendTime := launch.NETUnix - NotificationSendTimes[notifType]
			return NotificationTime{Type: notifType, SendTime: sendTime, LaunchId: launch.Id}
		}
	}

	log.Warn().Msgf("Returning an empty NotificationTime struct!")
	return NotificationTime{AllSent: true, LaunchId: launch.Id}
}

/*
Finds the next notification time from the launch cache.

Function goes over the notification states and finds the next notification
to send, returning a NotificationTime type with the send time and ID. */
func (cache *LaunchCache) NextNotificationTime() *NotificationTime {
	// Find first send-time from the launch cache
	earliestNotif := NotificationTime{SendTime: 0}

	tbdLaunchCount := 0
	for _, launch := range cache.Launches {
		// If launch time is TBD, don't notify
		if launch.Status.Abbrev != "TBD" {
			// Calculate the next upcoming send time for this launch
			next := launch.NextNotification()

			if next.AllSent {
				// If all notifications have already been sent, ignore
				log.Warn().Msgf("All notifiations have been sent for launch=%s", launch.Id)
				continue
			}

			if (next.SendTime < earliestNotif.SendTime) || (earliestNotif.SendTime == 0) {
				earliestNotif = next
			}
		} else {
			tbdLaunchCount++
		}
	}

	if earliestNotif.SendTime != 0 {
		log.Debug().Msgf("Got next notification send time: %d (%d sec from now) id=%s",
			earliestNotif.SendTime, earliestNotif.SendTime-time.Now().Unix(), earliestNotif.LaunchId)
		log.Debug().Msgf("Total of %d TBD launches out of %d launches", tbdLaunchCount, len(cache.Launches))
	} else {
		log.Warn().Msgf("Could not find next notification send time! TBD launches: %d out of %d",
			tbdLaunchCount, len(cache.Launches))
	}

	return &earliestNotif
}

/*
Extends the Launch struct to add a .PostponeNotify() method.
This allows us to write cleaner code.
*/
func (launch *Launch) PostponeNotify(postponedTo int) {
}

/**/
func (launch *Launch) Notify() {
}

/* Returns all values for a database insert */
func (launch *Launch) FieldValues() {
	// TODO complete
}
