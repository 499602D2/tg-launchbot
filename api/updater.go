package api

/*
	The updater updates the local database.
*/

import (
	"encoding/json"
	"errors"
	"fmt"
	"launchbot/config"
	"launchbot/db"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
)

// Performs an LL2 API call
func apiCall(client *resty.Client, useDevEndpoint bool) (db.LaunchUpdate, error) {
	const apiVersion = "2.2.0"
	const requestPath = "launch/upcoming"
	const apiParams = "mode=detailed&limit=30"

	// Set to true to use the ratelimited production end-point
	var endpoint string

	if useDevEndpoint {
		log.Warn().Msg("Using development endpoint")
		endpoint = "https://lldev.thespacedevs.com"
	} else {
		endpoint = "https://ll.thespacedevs.com"
	}

	// Construct the URL
	url := fmt.Sprintf("%s/%s/%s?%s", endpoint, apiVersion, requestPath, apiParams)

	// Do request
	resp, err := client.R().Get(url)

	// TODO resp.IsError() -> handle

	if err != nil {
		log.Error().Err(err).Msg("Error performing GET request")
		return db.LaunchUpdate{}, err
	}

	// Check status code
	if resp.StatusCode() != 200 {
		err = errors.New(fmt.Sprintf("Status code != 200 (code %d)", resp.StatusCode()))
		return db.LaunchUpdate{}, err
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

// Handles the API request flow, requesting new data and updating
// the cached and on-disk data.
func Updater(session *config.Session, scheduleNext bool) bool {
	log.Debug().Msg("Starting LL2 API updater...")

	// Create http-client
	client := resty.New()
	client.SetTimeout(time.Duration(1 * time.Minute))
	client.SetHeader("user-agent", "github.com/499602D2/launchbot-go")

	// Do API call
	update, err := apiCall(client, session.UseDevEndpoint)

	// TODO use api.errors
	if err != nil || len(update.Launches) == 0 {
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

	// Update hot launch cache
	session.LaunchCache.Update(launches)
	log.Debug().Msg("Hot launch cache updated")

	// Update on-disk database
	err = session.Db.Update(launches, true, true)
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

	// Save stats
	session.Telegram.Stats.LastApiUpdate = time.Now()

	// Schedule next API update, if configured
	if scheduleNext {
		return Scheduler(session, false)
	}

	return true
}

// Function that chrono calls when a scheduled API update runs.
func updateWrapper(session *config.Session) {
	log.Info().Msgf("Running scheduled update...")

	// Check return value of updater
	success := Updater(session, true)

	if !success {
		log.Warn().Msg("Updater failed: re-trying in 60 seconds...")

		// Retry twice
		for i := 1; ; i++ {
			success = Updater(session, true)

			if !success {
				retryAfter := 60*i ^ 2
				log.Warn().Msgf("Re-try number %d failed, trying again in %d minutes", i, retryAfter)
				time.Sleep(time.Duration(retryAfter) * time.Second)
			} else {
				log.Info().Msgf("Success after %d retries", i)
				break
			}
		}
	}
}
