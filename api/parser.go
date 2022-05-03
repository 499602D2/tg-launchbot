package api

import (
	"launchbot/db"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// GetHighestPriorityVideoLink finds the highest-priority link for the launch
func GetHighestPriorityVideoLink(links []db.ContentURL) *db.ContentURL {
	// If no links available, return a nil
	if len(links) == 0 {
		return &db.ContentURL{}
	}

	// The highest-priority link has the lowest value for Priority-field
	highestPriorityIndex := 0
	highestPriority := -1

	for idx, link := range links {
		log.Debug().Msgf("priority=%d, title=%s", link.Priority, link.Title)
		if link.Priority < highestPriority || highestPriority == -1 {
			highestPriority = link.Priority
			highestPriorityIndex = idx
		}
	}

	log.Debug().Msgf("Chose with prior=%d, title=%s", links[highestPriorityIndex].Priority, links[highestPriorityIndex].Title)
	return &links[highestPriorityIndex]
}

/* Checks if any launches were postponed */
func getPostponedLaunches(launches []*db.Launch) []*db.Launch {
	postponedLaunches := []*db.Launch{}

	return postponedLaunches
}

/* Checks if the NET of a launch slipped from one update to another. */
func netSlipped(cache *db.Cache, ll2launch *db.Launch) (bool, int64) {
	// If cache exists, use it
	// TODO implement
	if cache.Updated != (time.Time{}) {
		// Find launch
		//log.Info().Msg("[netSlipped()] cache exists")

		/* Launch not found in cache, check on disk

		The launch could have e.g. launched between the two checks, and might thus
		have disappeared from the /upcoming endpoint */
	} else {
		// Compare on disk
		//log.Info().Msg("[netSlipped()] cache not found, using disk")
	}

	return false, 0
}

/* Parses the LL2 launch update. */
func parseLaunchUpdate(cache *db.Cache, update *db.LaunchUpdate) ([]*db.Launch, error) {
	var utcTime time.Time
	var err error

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

		// Shorten launch pad names
		launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Satellite Launch Center ", "SLC-")
		launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Space Launch Complex ", "SLC-")
		launch.LaunchPad.Name = strings.ReplaceAll(launch.LaunchPad.Name, "Launch Complex ", "LC-")

		// Get the highest priority webcast URL
		highestPriorityUrl := GetHighestPriorityVideoLink(launch.VidURL)
		launch.WebcastLink = highestPriorityUrl.Url

		// If launch slipped, set postponed flag
		// TODO implement
		//postponed, by := netSlipped(cache, launch)
		//log.Info().Msgf("Launch postponed (%s): %s", postponed, by)

		/*
			if postponed {
				launch.Postponed = true
				launch.PostponedBy = by
			}*/

		// TODO If reused stage information, parse...

		//log.Debug().Msgf("[%2d] launch %s processed", i+1, ll2launch.Slug)
	}

	// Sort launches so they are ordered by NET
	sort.Slice(update.Launches, func(i, j int) bool {
		return update.Launches[i].NETUnix < update.Launches[j].NETUnix
	})

	return update.Launches, nil
}
