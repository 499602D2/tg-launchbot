package api

/*
	The updater updates the local database.
*/

import (
	"encoding/json"
	"fmt"
	"launchbot/config"
	"launchbot/db"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
)

/* Performs an LL2 API call */
func apiCall(client *resty.Client) (db.LaunchUpdate, error) {
	const apiVersion = "2.2.0"
	const requestPath = "launch/upcoming"
	const apiParams = "mode=detailed&limit=30"

	// Set to true to use the ratelimited production end-point
	useProdUrl := false
	var endpoint string

	if useProdUrl {
		endpoint = "https://ll.thespacedevs.com"
	} else {
		log.Warn().Msg("Using development endpoint")
		endpoint = "https://lldev.thespacedevs.com"
	}

	// Construct the URL
	url := fmt.Sprintf("%s/%s/%s?%s", endpoint, apiVersion, requestPath, apiParams)

	// Do request
	resp, err := client.R().Get(url)

	if err != nil {
		log.Error().Err(err).Msg("Error performing GET request")
		return db.LaunchUpdate{}, err
	}

	// Check status code
	if resp.StatusCode() != 200 {
		log.Warn().Msgf("Got status code %d", resp.StatusCode())
	}

	// Unmarshal into a launch update struct
	var update db.LaunchUpdate
	err = json.Unmarshal(resp.Body(), &update)

	if err != nil {
		log.Error().Err(err).Msg("Error unmarshaling JSON")
		return db.LaunchUpdate{}, err
	}

	// Set update count manually
	update.Count = len(update.Launches)

	return update, nil
}

/*
Handles the API request flow, requesting new data and updating
the cached and on-disk data.
*/
func Updater(session *config.Session, scheduleNext bool) bool {
	log.Debug().Msg("Starting LL2 API updater...")

	// Create http-client
	client := resty.New()
	client.SetTimeout(time.Duration(1 * time.Minute))
	client.SetHeader("user-agent", "github.com/499602D2/launchbot-go")

	// Do API call
	update, err := apiCall(client)

	// TODO use api.errors
	if err != nil {
		log.Error().Err(err).Msg("Error performing API update")
		return false
	}

	// Parse any relevant data before dumping to disk
	launches, err := parseLaunchUpdate(session.LaunchCache, &update)
	log.Debug().Msgf("Launch update parsed (%d launches)", update.Count)

	if err != nil {
		log.Error().Err(err).Msg("Error parsing launch update")
		return false
	}

	// Uncomment to force-send notifications
	/*
		for _, launch := range launches {
			if launch.Status.Abbrev == "Go" || launch.Status.Abbrev == "TBC" {
				launch.NETUnix = time.Now().Unix() + 4*60
				launch.Status.Abbrev = "Go"
				log.Warn().Msgf("Launch=%s modified to launch 30 seconds from now", launch.Slug)
				break
			}
		}
	*/

	// Update hot launch cache
	session.LaunchCache.Update(launches)
	log.Debug().Msg("Hot launch cache updated")

	// Update on-disk database
	err = session.Db.Update(launches)
	log.Info().Msg("Launch database updated")

	if err != nil {
		log.Error().Err(err).Msg("Error inserting launches to database")
		return false
	}

	// Clean the launch database
	err = session.Db.CleanSlippedLaunches()

	if err != nil {
		log.Error().Err(err).Msg("Error cleaning launch database")
		return false
	}

	// Parse for postponed launches, now that DB has been cleaned
	postponedLaunches := getPostponedLaunches(launches)

	if len(postponedLaunches) != 0 {
		// TODO send notifications for postponed launches
		// TODO how to handle notification flow? just return false and abort?
		log.Info().Msgf("%d launches were postponed", len(postponedLaunches))
	} else {
		log.Debug().Msg("No launches were postponed")
	}

	// Schedule next API update, if configured
	if scheduleNext {
		return Scheduler(session)
	}

	return true
}

/* Function that chrono calls when a scheduled API update runs. */
func updateWrapper(session *config.Session) {
	log.Info().Msgf("Running scheduled update...")

	// Check return value of updater
	success := Updater(session, true)

	if !success {
		// TODO define retry time-limit based on error codes (api/errors.go)
		log.Warn().Msg("Running updater failed: retrying in 60 seconds...")

		// Retry twice
		// TODO use expontential back-off?)
		for i := 1; i <= 3; i++ {
			success = Updater(session, true)
			if !success {
				log.Warn().Msgf("Re-try number %d failed, trying again in %d seconds", i, 60)
				time.Sleep(time.Second * 60)
			} else {
				log.Info().Msgf("Success after %d retries", i)
				break
			}
		}

		// TODO if failed, notify admin + logs
	}
}
