package api

import (
	"launchbot/db"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jdkato/prose/v2"
	"github.com/rs/zerolog/log"
)

// Finds the highest-priority link for the launch
func getHighestPriorityVideoLink(links []db.ContentURL) *db.ContentURL {
	// If no links available, return a nil
	if len(links) == 0 {
		return &db.ContentURL{}
	}

	// The highest-priority link has the lowest value for priority-field
	highestPriorityIndex, highestPriority := 0, -1

	for idx, link := range links {
		if link.Priority < highestPriority || highestPriority == -1 {
			highestPriority = link.Priority
			highestPriorityIndex = idx
		}
	}

	return &links[highestPriorityIndex]
}

// Checks if the NET of a launch slipped from one update to another.
// Returns a bool indicating if this happened, and a Postpone{} characterizing the NET slip.
func netParser(cache *db.Cache, freshLaunch *db.Launch) (bool, db.Postpone) {
	/* Launch not found in cache, check on disk

	The launch could have e.g. launched between the two checks, and might thus
	have disappeared from the /upcoming endpoint */
	cacheLaunch, ok := cache.LaunchMap[freshLaunch.Id]

	if !ok {
		// Typically, a launch is not cached if it has slipped outside of range, or has already launched.
		// This also frequently occurs when switching back and forth between the main- and dev-endpoint of LL2.
		if time.Now().Unix() > freshLaunch.NETUnix {
			log.Debug().Msgf("(OK) Launch is in the past, and not found in cache slug=%s", freshLaunch.Slug)
		} else {
			log.Warn().Msgf("Launch with slug=%s not found in cache, but NET in the future", freshLaunch.Slug)
		}

		return false, db.Postpone{}
	}

	// NETs differ and launch has not launched yet
	if freshLaunch.NETUnix != cacheLaunch.NETUnix && !freshLaunch.Launched {
		netSlip := freshLaunch.NETUnix - cacheLaunch.NETUnix

		// If no notifications have been sent, the postponement does not matter
		if !cacheLaunch.NotificationState.AnyNotificationsSent() {
			log.Debug().Msgf("Launch NETs don't match, but no notifications have been sent: returning false")
			return false, db.Postpone{}
		}

		// Check if this postponement resets any notification states
		anyReset, resetStates := cacheLaunch.AnyStatesResetByNetSlip(netSlip)
		if anyReset {
			// Launch had one or more notification states reset: all handled behind the scenes.
			log.Debug().Msgf("Launch NET moved, and a notification state was reset")
			return true, db.Postpone{PostponedBy: netSlip, ResetStates: resetStates}
		}

		log.Debug().Msgf("Launch NET moved, but no states were reset despite notifications having been previously sent")
	}

	return false, db.Postpone{}
}

// Process a single launch; function is run concurrently.
func processLaunch(launch *db.Launch, launchUpdate *db.LaunchUpdate, idx int, cache *db.Cache, wg *sync.WaitGroup) {
	// Parse the datetime string as RFC3339 into a time.Time object in UTC
	utcTime, err := time.ParseInLocation(time.RFC3339, launch.NET, time.UTC)

	if err != nil {
		log.Error().Err(err).Msg("Error parsing RFC3339 launch time")
	}

	// Convert to unix time, store
	launch.NETUnix = time.Time.Unix(utcTime)

	// Set launched status, 3: success, 4: failure, 6: in-flight, 7: partial failure
	switch launch.Status.Id {
	case 3, 4, 6, 7:
		launch.Launched = true
	}

	// Shorten description, by keeping the first two sentences
	// Extremely slow, approx. 300 ms per launch.
	document, err := prose.NewDocument(launch.Mission.Description)

	if err != nil {
		log.Error().Err(err).Msgf("Processing description for launch=%s failed", launch.Id)
	} else {
		if len(document.Sentences()) > 2 {
			// Prepare the array
			sentences := make([]string, 2)

			// More than two sentences: move their text content to an array
			for i, sentence := range document.Sentences() {
				sentences[i] = sentence.Text

				if i == 1 {
					break
				}
			}

			// Join first two sentences
			launch.Mission.Description = strings.Join(sentences[:2], " ")
		}
	}

	// Shorten launch pad names
	launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Satellite Launch Center ", "SLC-")
	launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Space Launch Complex ", "SLC-")
	launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Launch Complex ", "LC-")

	// Get the highest priority webcast URL
	highestPriorityUrl := getHighestPriorityVideoLink(launch.VidURL)
	launch.WebcastLink = highestPriorityUrl.Url

	// TODO If reused stage information, parse...

	// If launch slipped enough to reset a notification state, save it
	wasPostponed, postponeStatus := netParser(cache, launch)

	// Lock mutex so we can save the launch
	launchUpdate.Mutex.Lock()
	defer launchUpdate.Mutex.Unlock()

	if wasPostponed {
		// If launch was postponed, add it to the update
		launchUpdate.Postponed[launch] = postponeStatus
	}

	// Update launch in launchUpdate (Mutex is locked so this is thread-safe)
	launchUpdate.Launches[idx] = launch

	// Worker done
	wg.Done()
}

// Parses the LL2 launch update, returning the parsed launches and any launches that were postponed
func parseLaunchUpdate(cache *db.Cache, update *db.LaunchUpdate) ([]*db.Launch, map[*db.Launch]db.Postpone, error) {
	// Process launches concurrently
	var wg sync.WaitGroup

	// Add concurrent workers (results in a +300 % speed-up compared to synchronous)
	wg.Add(len(update.Launches))

	// Loop over launches and spawn go-routines
	for idx, launch := range update.Launches {
		go processLaunch(launch, update, idx, cache, &wg)
	}

	// Wait for all processes to finish
	wg.Wait()

	// Sort launches so they are ordered by NET
	sort.Slice(update.Launches, func(i, j int) bool {
		return update.Launches[i].NETUnix < update.Launches[j].NETUnix
	})

	return update.Launches, update.Postponed, nil
}
