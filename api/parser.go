package api

import (
	"launchbot/db"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jdkato/prose/v2"
	"github.com/rs/zerolog/log"
	"golang.org/x/sys/cpu"
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

// Parses the launcher info we receive from the API into something more digestible
func parseLauncherInfo(launch *db.Launch) {
	launch.Rocket.Launchers.Count = len(launch.Rocket.UnparsedLauncherInfo)
	launch.Rocket.Launchers.Core = db.Launcher{}
	launch.Rocket.Launchers.Boosters = db.Launcher{}

	for _, launcher := range launch.Rocket.UnparsedLauncherInfo {
		if strings.ToLower(launcher.Type) == "core" {
			// This init is ugly, but we can't use same types due to JSON/Gorm funkiness
			launch.Rocket.Launchers.Core = db.Launcher{
				Serial:          launcher.Detailed.Serial,
				Reused:          launcher.Reused,
				FlightNumber:    launcher.FlightNumber,
				Flights:         launcher.Detailed.Flights,
				FirstLaunchDate: launcher.Detailed.FirstLaunchDate,
				LastLaunchData:  launcher.Detailed.LastLaunchDate,
				LandingAttempt:  launcher.Landing.Attempt,
				LandingSuccess:  launcher.Landing.Success,
				LandingLocation: launcher.Landing.Location,
				LandingType:     launcher.Landing.Type,
			}
		} else {
			log.Warn().Msgf("TODO: not parsing a launcher that is not a core (type=%s)",
				launcher.Type)
		}
	}
}

// Checks if the NET of a launch slipped from one update to another.
// Returns a bool indicating if this happened, and a Postpone{} characterizing the NET slip.
func netParser(cache *db.Cache, freshLaunch *db.Launch) (bool, db.Postpone) {
	// Load the cached launch (effectively the old version)
	cacheLaunch, ok := cache.LaunchMap[freshLaunch.Id]

	if !ok {
		/* A launch is uncached if it has slipped outside of range, or has already launched.
		This also frequently occurs when switching back and forth between the main- and dev-endpoint of LL2,
		or if a launch is simply new and is seen for the first time. */

		return false, db.Postpone{}
	}

	// NETs differ and launch has not launched yet
	if freshLaunch.NETUnix != cacheLaunch.NETUnix && !freshLaunch.Launched {
		netSlip := freshLaunch.NETUnix - cacheLaunch.NETUnix

		// If no notifications have been sent, the postponement does not matter
		if !cacheLaunch.NotificationState.AnyNotificationsSent() {
			log.Debug().Msgf("Launch NETs don't match (by %d sec), but no notifications sent. Returning false (%s)",
				netSlip, cacheLaunch.Slug,
			)

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
func processLaunch(launch *db.Launch, update *db.LaunchUpdate, idx int, cache *db.Cache, wg *sync.WaitGroup) {
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

	/* Shorten description, by keeping the first two sentences, and
	disable all heavy NLP functions, except for sentence-splitting */
	document, err := prose.NewDocument(
		launch.Mission.Description, prose.WithExtraction(false),
		prose.WithTagging(false), prose.WithTokenization(false),
	)

	if err != nil {
		log.Error().Err(err).Msgf("Processing description for launch=%s failed", launch.Id)
	} else {
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

	// Shorten launch pad names
	launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Satellite Launch Center ", "SLC-")
	launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Space Launch Complex ", "SLC-")
	launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Launch Complex ", "LC-")

	// Get the highest priority webcast URL
	highestPriorityUrl := getHighestPriorityVideoLink(launch.VidURL)
	launch.WebcastLink = highestPriorityUrl.Url

	// If booster/launcher information, parse it
	// TODO implement for multiple boosters (e.g. Falcon Heavy)
	if len(launch.Rocket.UnparsedLauncherInfo) != 0 {
		parseLauncherInfo(launch)
	} else {
		launch.Rocket.Launchers = db.Launchers{Count: 0}
	}

	// If launch slipped enough to reset a notification state, save it
	wasPostponed, postponeStatus := netParser(cache, launch)

	// Lock mutex so we can save the launch
	update.Mutex.Lock()
	defer update.Mutex.Unlock()

	if wasPostponed {
		// If launch was postponed, add it to the update
		update.Postponed[launch] = postponeStatus
	}

	// Update launch in launchUpdate (Mutex is locked so this is thread-safe)
	update.Launches[idx] = launch

	// Worker done
	wg.Done()
}

// Parses the LL2 launch update, returning the parsed launches and any launches that were postponed
func parseLaunchUpdate(cache *db.Cache, update *db.LaunchUpdate) ([]*db.Launch, map[*db.Launch]db.Postpone, error) {
	var (
		activeRoutines int
		wg             sync.WaitGroup
	)

	// Maximum workers, based on core-count and CPU architecture
	maxConcurrentThreads := runtime.NumCPU()

	if cpu.X86.HasAVX {
		// Allow more threads on x86 chips, and avoid choking narrow arm-chips
		maxConcurrentThreads *= 2
	}

	// Avoid spawning too many routines
	if maxConcurrentThreads > len(update.Launches) {
		maxConcurrentThreads = len(update.Launches)
	}

	// Add concurrent workers (results in a +300 % speed-up compared to synchronous)
	wg.Add(maxConcurrentThreads)

	// Loop over launches and spawn go-routines
	for idx, launch := range update.Launches {
		go processLaunch(launch, update, idx, cache, &wg)
		activeRoutines++

		// If enough workers spawned, wait for them to finish
		if activeRoutines == maxConcurrentThreads {
			wg.Wait()
			activeRoutines = 0
		}

		// Add workers depending on index
		if activeRoutines == 0 && (idx < len(update.Launches)-1) {
			if len(update.Launches)-(idx+1) >= maxConcurrentThreads {
				// Enough indices left to add max amount of routines
				wg.Add(maxConcurrentThreads)
			} else {
				// If almost done, only add enough routines to get to the last index
				maxConcurrentThreads = len(update.Launches) - idx - 1
				wg.Add(maxConcurrentThreads)
			}
		}
	}

	// Sort launches so they are ordered by NET
	sort.Slice(update.Launches, func(i, j int) bool {
		return update.Launches[i].NETUnix < update.Launches[j].NETUnix
	})

	return update.Launches, update.Postponed, nil
}
