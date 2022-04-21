package db

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type Cache struct {
	Launches  []*Launch          // An ordered list of launches
	LaunchMap map[string]*Launch // Maps the launch ID to the launch object
	Updated   time.Time          // Time the cache was last updated
	Mutex     sync.Mutex
}

// Updates cache with a list of launches
func (cache *Cache) Update(launches []*Launch) {
	cache.Mutex.Lock()
	defer cache.Mutex.Unlock()

	// Remove the launch list
	cache.Launches = []*Launch{}

	// Preserve old launch ID map before deleting it
	oldCache := cache.LaunchMap
	cache.LaunchMap = make(map[string]*Launch)

	// Re-insert all launches into the launch list and launch map
	for _, launch := range launches {
		// Pull old launch
		oldLaunch, ok := oldCache[launch.Id]

		// Copy notification states, if old launch exists
		if ok {
			launch.Notifications = oldLaunch.Notifications
		}

		// Save new launch
		cache.Launches = append(cache.Launches, launch)
		cache.LaunchMap[launch.Id] = launch

		if !ok {
			// TODO REMOVE ONCE PARSING IMPLEMENTED
			testNotifs := NotificationStates{
				"24hour": false, "12hour": false,
				"1hour": false, "5min": false,
			}

			cache.LaunchMap[launch.Id].Notifications = testNotifs
		}
	}

	cache.Updated = time.Now()
}

// Populates the cache from database
func (cache *Cache) Populate(update *LaunchUpdate) {
	cache.Mutex.Lock()

	// TODO implement
	// - select all launches that have not launched
	// - create a list of launch objects from the returned rows
	// - do a cache.Update()

	// TODO load notification states from the database for all launches
	// (launch.Notifications)
	cache.Mutex.Unlock()
}

// Finds the next notification send-time from the launch cache.
// Function goes over the notification states and finds the next notification
// to send, returning a NotificationTime type with the send-time and all launch
// IDs associated with this send-time.
func (cache *Cache) FindNext() *Notification {
	// Find first send-time from the launch cache
	earliestTime := int64(0)
	tbdLaunchCount := 0

	/* Returns a list of notification times
	(only more than one if two+ notifs share the same send time) */
	notificationTimes := make(map[int64][]Notification)

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

		return &Notification{SendTime: 0, Count: 0}
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
		weighedNotifs := make(map[int]Notification)

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
