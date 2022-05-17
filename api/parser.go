package api

import (
	"launchbot/db"
	"sort"
	"strings"
	"time"

	"github.com/jdkato/prose/v2"
	"github.com/rs/zerolog/log"
)

// GetHighestPriorityVideoLink finds the highest-priority link for the launch
func GetHighestPriorityVideoLink(links []db.ContentURL) *db.ContentURL {
	// If no links available, return a nil
	if len(links) == 0 {
		return &db.ContentURL{}
	}

	// The highest-priority link has the lowest value for priority-field
	highestPriorityIndex := 0
	highestPriority := -1

	for idx, link := range links {
		if link.Priority < highestPriority || highestPriority == -1 {
			highestPriority = link.Priority
			highestPriorityIndex = idx
		}
	}

	return &links[highestPriorityIndex]
}

/* Checks if the NET of a launch slipped from one update to another. */
func netSlipped(cache *db.Cache, freshLaunch *db.Launch) (bool, db.Postpone) {
	/* Launch not found in cache, check on disk

	The launch could have e.g. launched between the two checks, and might thus
	have disappeared from the /upcoming endpoint */
	cacheLaunch, ok := cache.LaunchMap[freshLaunch.Id]

	if !ok {
		log.Debug().Msgf("Launch with id=%s not found in cache", freshLaunch.Id)
		return false, db.Postpone{}
	}

	// NETs differ and launch has not launched yet
	if freshLaunch.NETUnix != cacheLaunch.NETUnix && !freshLaunch.Launched {
		netSlip := freshLaunch.NETUnix - cacheLaunch.NETUnix

		// If no notifications have been sent, the postponement does not matter
		if !cacheLaunch.NotificationState.AnyNotificationsSent() {
			return false, db.Postpone{}
		}

		// Check if this postponement resets any notification states
		anyReset, resetStates := cacheLaunch.AnyStatesResetByNetSlip(netSlip)
		if anyReset {
			// Launch had one or more notification states reset: all handled behind the scenes.
			return true, db.Postpone{PostponedBy: netSlip, ResetStates: resetStates}
		}
	}

	return false, db.Postpone{}
}

/* Parses the LL2 launch update. */
func parseLaunchUpdate(cache *db.Cache, update *db.LaunchUpdate) ([]*db.Launch, map[*db.Launch]db.Postpone, error) {
	var utcTime time.Time
	var err error

	postponedLaunches := map[*db.Launch]db.Postpone{}

	// Loop over launches and do any required operations
	for _, launch := range update.Launches {
		// Parse the datetime string as RFC3339 into a time.Time object in UTC
		utcTime, err = time.ParseInLocation(time.RFC3339, launch.NET, time.UTC)

		if err != nil {
			log.Error().Err(err).Msg("Error parsing RFC3339 launch time")
		}

		// Convert to unix time, store
		launch.NETUnix = time.Time.Unix(utcTime)

		// Set launched status
		// 3: success, 4: failure, 6: in-flight, 7: partial failure
		switch launch.Status.Id {
		case 3, 4, 6, 7:
			launch.Launched = true
		}

		// Shorten description, by keeping the first two sentences
		document, err := prose.NewDocument(launch.Mission.Description)
		sentences := []string{}

		if err != nil {
			log.Error().Err(err).Msgf("Processing description for launch=%s failed", launch.Id)
		} else {
			if len(document.Sentences()) > 2 {
				// More than two sentences: move their text content to an array
				for _, sentence := range document.Sentences() {
					sentences = append(sentences, sentence.Text)
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
		highestPriorityUrl := GetHighestPriorityVideoLink(launch.VidURL)
		launch.WebcastLink = highestPriorityUrl.Url

		// TODO If reused stage information, parse...

		// If launch slipped enough to reset a notification state, save it
		postponed, postponeStatus := netSlipped(cache, launch)

		if postponed {
			postponedLaunches[launch] = postponeStatus
		}
	}

	// Sort launches so they are ordered by NET
	sort.Slice(update.Launches, func(i, j int) bool {
		return update.Launches[i].NETUnix < update.Launches[j].NETUnix
	})

	return update.Launches, postponedLaunches, nil
}
