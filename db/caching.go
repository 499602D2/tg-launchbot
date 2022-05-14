package db

import (
	"errors"
	"launchbot/users"
	"sort"
	"sync"
	"time"

	"github.com/hako/durafmt"
	"github.com/rs/zerolog/log"
)

type Cache struct {
	Launches  []*Launch          // An ordered list of launches
	LaunchMap map[string]*Launch // Maps the launch ID to the launch object
	Updated   time.Time          // Time the cache was last updated
	Users     *users.UserCache
	Database  *Database
	Mutex     sync.Mutex
}

// Updates cache with a list of fresh launches
func (cache *Cache) Update(launches []*Launch) {
	cache.Mutex.Lock()
	defer cache.Mutex.Unlock()

	if cache.Updated == (time.Time{}) {
		// If cache has not been initialized yet, populate it first so we have all
		// the notification states we need
		cache.Mutex.Unlock()
		cache.Populate()
		cache.Mutex.Lock()
	}

	// Preserve old launch-ID map before deleting it, so we can reuse notif. states
	oldCache := cache.LaunchMap

	// Remove the launch list (pointers are preserved in LaunchMap)
	cache.Launches = []*Launch{}

	// Initialize the launch-ID -> *launch -map
	cache.LaunchMap = make(map[string]*Launch)

	// Re-insert all launches into the launch list and launch map
	for _, launch := range launches {
		// If launch has launched, ignore
		if launch.Launched || launch.NETUnix < time.Now().Unix() {
			log.Warn().Msgf("Ignoring launch with launched=1 or NETUnix < now in cache.Update(), slug=%s", launch.Slug)
			continue
		}

		// Pull old launch
		oldLaunch, ok := oldCache[launch.Id]

		if ok {
			// Copy notification states if old launch exists
			launch.NotificationState = oldLaunch.NotificationState
		} else {
			// If states don't exist, initialize from struct's values
			launch.NotificationState = launch.NotificationState.UpdateMap()
		}

		// Save new launch
		cache.Launches = append(cache.Launches, launch)
		cache.LaunchMap[launch.Id] = launch
	}

	cache.Updated = time.Now()
}

// Populates the cache from database
func (cache *Cache) Populate() {
	cache.Mutex.Lock()

	// Save found launches to a slice
	var launches []*Launch

	// Find all launches that have not launched
	result := cache.Database.Conn.Model(&Launch{}).Where("launched = ? AND net_unix > ?", 0, time.Now().Unix()).Find(&launches)

	// TODO handle other database errors
	switch result.Error {
	case nil:
		break
	default:
		log.Error().Err(result.Error).Msg("Encountered error while populating launch cache")
	}

	// Assign to cache
	cache.Launches = launches

	// Initialize the launch-ID -> *launch -map
	cache.LaunchMap = make(map[string]*Launch)

	// Loop over launches, init cache map + notification states
	for _, launch := range cache.Launches {
		// Assign to map
		cache.LaunchMap[launch.Id] = launch

		// Init the notification states
		launch.NotificationState = launch.NotificationState.UpdateMap()
	}

	// Finally, sort the cache by NET
	sort.Slice(cache.Launches, func(i, j int) bool {
		return cache.Launches[i].NETUnix < cache.Launches[j].NETUnix
	})

	log.Info().Msgf("Cache populated with %d launches", len(launches))
	cache.Mutex.Unlock()
}

// Finds the next notification send-time from the launch cache.
// Function goes over the notification states, finding the next notification
// to send. Returns a Notification-type, with the send-time and all launch
// IDs associated with this send-time.
func (cache *Cache) FindNextNotification() *Notification {
	// Find first send-time from the launch cache
	earliestTime := int64(0)
	tbdLaunchCount := 0

	// Returns a list of notification times
	// (only more than one if two+ notifs share the same send time)
	notificationTimes := make(map[int64][]Notification)

	// How much the send time is allowed to slip, in minutes
	allowedNetSlip := time.Duration(-5) * time.Minute

	for _, launch := range cache.Launches {
		// If launch time is TBD or in the past, don't notify
		if launch.Status.Abbrev == "Go" || launch.Status.Abbrev == "TBC" {
			// Calculate the next upcoming send time for this launch
			next := launch.NextNotification(cache.Database)

			if next.AllSent {
				// If all notifications have already been sent, ignore
				log.Warn().Msgf("All notifications have been sent for launch=%s", launch.Id)
				continue
			}

			// Verify the launch-time is not in the past by more than the allowed slip window
			if allowedNetSlip.Seconds() > time.Until(time.Unix(next.SendTime, 0)).Seconds() {
				log.Warn().Msgf("[cache.findNext()] Launch %s is more than 5 minutes into the past",
					next.LaunchName)
				continue
			}

			if (next.SendTime < earliestTime) || (earliestTime == 0) {
				// If send-time is smaller than last earliestTime, delete old key and insert
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
		toNext := durafmt.Parse(time.Until(time.Unix(earliestTime, 0))).LimitFirstN(2)
		log.Debug().Msgf("Got next notification send time (%s from now), %d launch(es))",
			toNext, len(notificationTimes[earliestTime]))

		// Print launch names in logs
		for _, notif := range notificationTimes[earliestTime] {
			log.Debug().Msgf("➙ %s (%s)", notif.LaunchName, notif.LaunchId)
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
			"24h": 1, "12h": 2,
			"1h": 3, "5min": 4,
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

// Computes the time interval until next scheduled API update
func (cache *Cache) NextScheduledUpdateIn() (time.Duration, *Notification) {
	// Get next notification
	notification := cache.FindNextNotification()
	timeUntil := time.Until(time.Unix(notification.SendTime, 0))

	// The time interval to wait until next API update
	var autoUpdateIn time.Duration

	/* Decide next update time based on the notification's type, and based on
	the time until said notification. Do note, that this is only a regular,
	scheduled check. A final check will be performed just before a notification
	is sent, independent of these scheduled checks. */
	switch notification.Type {
	case "24h":
		// 24-hour window (?h ... 24h)
		if timeUntil.Hours() >= 6 {
			autoUpdateIn = time.Hour * 6
		} else {
			autoUpdateIn = time.Hour * 3
		}
	case "12h":
		// 12-hour window (24h ... 12h)
		autoUpdateIn = time.Hour * 3
	case "1h":
		// 1-hour window (12h ... 1h)
		if timeUntil.Hours() >= 4 {
			autoUpdateIn = time.Hour * 2
		} else {
			autoUpdateIn = time.Hour
		}
	case "5min":
		// 5-min window (1h ... 5 min), less than 55 minutes
		autoUpdateIn = time.Minute * 15
	default:
		// Default case, needed for debugging without a working database
		log.Error().Msgf("Next notification's type fell through: %#v", notification)
		autoUpdateIn = time.Hour * 6
	}

	return autoUpdateIn, notification
}

// Finds a launch from the cache by a launch ID. If not present in the cache,
// checks the disk.
func (cache *Cache) FindLaunchById(id string) (*Launch, error) {
	// Find the launch from the LaunchMap
	launch, ok := cache.LaunchMap[id]

	if ok {
		return launch, nil
	}

	// Launch not found in cache: check the disk, avoid SQL injection
	// https://gorm.io/docs/query.html#Retrieving-objects-with-primary-key
	thisLaunch := Launch{}
	result := cache.Database.Conn.Model(&thisLaunch).First(&thisLaunch, "id = ?", id)

	if result.Error != nil {
		log.Error().Err(result.Error).Msgf("Error searching for launch in the database with id=%s", id)
		err := errors.New("Launch not found")
		return nil, err
	}

	// Launch was found: return it
	return &thisLaunch, nil
}
