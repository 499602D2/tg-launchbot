package ll2

import (
	"fmt"
	"launchbot/bots"
	"launchbot/db"
	"launchbot/users"
	"math"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type NotificationTime struct {
	Type       string // In (24hour, 12hour, 1hour, 5min)
	SendTime   int64  // Unix-time of the notification
	AllSent    bool   // All notifications sent already?
	LaunchId   string
	LaunchName string

	Count int      // If more than one, list their count
	IDs   []string // If more than one, include their IDs here
}

// func (cache *LaunchCache) FindAllWithNet(net int64) []*Launch {
// 	launches := []*Launch{}

// 	for _, launch := range cache.Launches {

// 	}
// }

/* Returns the first unsent notification type for the launch. */
func (launch *Launch) NextNotification() NotificationTime {
	// TODO do this smarter instead of re-declaring a billion times
	NotificationSendTimes := map[string]time.Duration{
		"24hour": time.Duration(24) * time.Hour,
		"12hour": time.Duration(12) * time.Hour,
		"1hour":  time.Duration(1) * time.Hour,
		"5min":   time.Duration(5) * time.Minute,
	}

	for notifType, sent := range launch.Notifications {
		// Map starts from 24hour, goes down to 5min
		if sent == false {
			// How many seconds before NET the notification is sent
			secBeforeNet, ok := NotificationSendTimes[notifType]

			if !ok {
				log.Error().Msgf("Error parsing notificationType for %s: %s",
					launch.Id, notifType)
				continue
			}

			// Calculate send-time from NET
			// log.Debug().Msgf("type: %s, secBeforeNet: %s", notifType, secBeforeNet.String())
			// log.Debug().Msgf("NET time: %s", time.Unix(launch.NETUnix, 0).String())
			sendTime := launch.NETUnix - int64(secBeforeNet.Seconds())

			if sendTime-time.Now().Unix() < 0 {
				// Calculate how many minutes the notification was missed by
				missedBy := time.Duration(math.Abs(float64(time.Now().Unix()-sendTime))) * time.Second

				// TODO implement launch.ClearMissedNotifications + database update
				log.Warn().Msgf("[%s] Send-time is in the past by %.2f minutes", launch.Id, missedBy.Minutes())

				if notifType == "5min" {
					if missedBy > time.Minute*time.Duration(5) {
						log.Warn().Msgf("Missed notifications for id=%s", launch.Id)
						return NotificationTime{AllSent: true, LaunchId: launch.Id, LaunchName: launch.Name, Count: 0}
					} else {
						log.Info().Msgf("[%s] Launch time less than 5 min in the past: modifying send-time", launch.Id)

						// Modify to send in 15 seconds
						sendTime = time.Now().Unix() + 15

						return NotificationTime{Type: notifType, SendTime: sendTime,
							LaunchId: launch.Id, LaunchName: launch.Name, Count: 1}
					}
				}

				continue
			}

			return NotificationTime{Type: notifType, SendTime: sendTime,
				LaunchId: launch.Id, LaunchName: launch.Name, Count: 1}
		}
	}

	return NotificationTime{AllSent: true, LaunchId: launch.Id, LaunchName: launch.Name, Count: 0}
}

/*
Finds the next notification time from the launch cache.

Function goes over the notification states and finds the next notification
to send, returning a NotificationTime type with the send time and ID. */
func (cache *LaunchCache) NextNotificationTime() *NotificationTime {
	// Find first send-time from the launch cache
	earliestTime := int64(0)
	tbdLaunchCount := 0

	/* Returns a list of notification times
	(only more than one if two+ notifs share the same send time) */
	notificationTimes := make(map[int64][]NotificationTime)

	// How much the send time is allowed to slip, in minutes
	allowedNetSlip := time.Duration(-5) * time.Minute

	for _, launch := range cache.Launches {
		// If launch time is TBD/TBC or in the past, don't notify
		if launch.Status.Abbrev == "Go" {
			// Calculate the next upcoming send time for this launch
			next := launch.NextNotification()

			if next.AllSent {
				// If all notifications have already been sent, ignore
				// log.Warn().Msgf("All notifications have been sent for launch=%s", launch.Id)
				continue
			}

			// Verify the launch-time is not in the past by more than the allowed slip window
			if allowedNetSlip.Seconds() > time.Until(time.Unix(next.SendTime, 0)).Seconds() {
				log.Warn().Msgf("Launch %s is more than 5 minutes into the past",
					next.LaunchName)
				continue
			}

			if (next.SendTime < earliestTime) || (earliestTime == 0) {
				// If time is smaller than last earliestTime, delete old key and insert
				delete(notificationTimes, earliestTime)
				earliestTime = next.SendTime

				// Insert into the map's list
				notificationTimes[next.SendTime] = append(notificationTimes[next.SendTime], next)
			} else if next.SendTime == earliestTime {
				// Alternatively, if the time is equal, we have two launches overlapping
				notificationTimes[next.SendTime] = append(notificationTimes[next.SendTime], next)
			}
		} else {
			tbdLaunchCount++
		}
	}

	// If time is non-zero, there's at least one non-TBD launch
	if earliestTime != 0 {
		// Calculate time until notification(s)
		toNotif := time.Until(time.Unix(earliestTime, 0))

		log.Debug().Msgf("Got next notification send time (%s from now), %d launches)",
			toNotif.String(), len(notificationTimes[earliestTime]))

		// Print launch names in logs
		for n, l := range notificationTimes[earliestTime] {
			log.Debug().Msgf("[%d] %s (%s)", n+1, l.LaunchName, l.LaunchId)
		}
	} else {
		log.Warn().Msgf("Could not find next notification send time. No-Go launches: %d out of %d",
			tbdLaunchCount, len(cache.Launches))

		return &NotificationTime{SendTime: 0, Count: 0}
	}

	// Select the list of launches for the earliest timestamp
	notificationList := notificationTimes[earliestTime]

	// If more then one, prioritize them
	if len(notificationList) > 1 {
		// Add more weight to the latest notifications
		timeWeights := map[string]int{
			"24hour": 1, "12hour": 2,
			"1hour": 3, "5min": 4,
		}

		// Keep track of largest encountered key (timeWeight)
		maxTimeWeight := 0

		// Map the weights to a single NotificationTime type
		weighedNotifs := make(map[int]NotificationTime)

		// Loop over the launches we found at this timestamp
		for _, notifTime := range notificationList {
			// Add to the weighed map
			weighedNotifs[timeWeights[notifTime.Type]] = notifTime

			// If weight is greater than the largest encountered, update
			if timeWeights[notifTime.Type] > maxTimeWeight {
				maxTimeWeight = timeWeights[notifTime.Type]
			}
		}

		// Assign highest-value key found as the primary notification
		firstNotif := weighedNotifs[maxTimeWeight]
		firstNotif.Count = len(notificationList)
		firstNotif.IDs = append(firstNotif.IDs, firstNotif.LaunchId)

		// Add other launches to the list
		for _, notifTime := range notificationList {
			if notifTime.LaunchId != firstNotif.LaunchId {
				firstNotif.IDs = append(firstNotif.IDs, notifTime.LaunchId)
			}
		}

		log.Debug().Msgf("Total of %d launches in the notification list after parsing:",
			len(firstNotif.IDs))

		for i, id := range firstNotif.IDs {
			log.Debug().Msgf("[%d] %s", i+1, id)
		}

		return &firstNotif
	}

	// Otherwise, we only have one notification: return it
	onlyNotif := notificationList[0]
	onlyNotif.IDs = append(onlyNotif.IDs, onlyNotif.LaunchId)
	return &onlyNotif
}

/*
Extends the Launch struct to add a .PostponeNotify() method.
This allows us to write cleaner code.
*/
func (launch *Launch) PostponeNotify(postponedTo int) {
}

/* Pulls recipients for this notification type from the DB */
func (launch *Launch) GetRecipients(db *db.Database, notifType NotificationTime) *users.UserList {
	// TODO Implement
	recipients := users.UserList{Platform: "tg", Users: []*users.User{}}
	user := users.User{Platform: recipients.Platform, Id: db.Owner}

	recipients.Add(user, true)

	return &recipients
}

/* Creates and queues a notification */
func (launch *Launch) Notify(db *db.Database) *bots.Sendable {
	// TODO for the message construction: use "real ETA" for 5 min notification

	notification := launch.NextNotification()

	// Map notification type to a header
	header := map[string]string{
		"24hour": "24 hours",
		"12hour": "12 hours",
		"1hour":  "60 minutes",
		"5min":   "5 minutes",
	}[notification.Type]

	name := launch.Mission.Name
	if name == "" {
		name = strings.Trim(strings.Split(launch.Name, "|")[0], " ")
	}

	// Do a simple notification string
	text := fmt.Sprintf(
		`🚀 %s is launching in %s
		Launch ID: %s`,
		name, header, launch.Id,
	)

	// Trim whitespace
	text = strings.TrimSpace(text)

	// Message
	msg := bots.Message{TextContent: &text}

	// TODO Get recipients
	recipients := launch.GetRecipients(db, notification)

	// Create sendable
	sendable := bots.Sendable{
		Priority: 1, Type: "notification", Message: &msg, Recipients: recipients,
		RateLimit: 4.0,
	}

	// Set as sent
	// TODO set earlier ones as sent, too, if they were missed
	iterMap := map[string]string{
		"5min":   "1hour",
		"1hour":  "12hour",
		"12hour": "24hour",
	}

	passed := false
	for curr, next := range iterMap {
		if !passed && curr == notification.Type {
			launch.Notifications[curr] = true
			passed = true
		}

		if passed {
			if launch.Notifications[next] == false {
				log.Debug().Msgf("Set %s to true for launch=%s", next, launch.Id)
				launch.Notifications[next] = true
			}
		}
	}

	return &sendable
}

/* Returns all values for a database insert */
func (launch *Launch) FieldValues() {
	// TODO complete
}
