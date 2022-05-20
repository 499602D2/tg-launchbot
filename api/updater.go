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
	"github.com/hako/durafmt"
	"github.com/rs/zerolog/log"
)

// Performs an LL2 API call
func apiCall(client *resty.Client, useDevEndpoint bool) (*db.LaunchUpdate, error) {
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
		return &db.LaunchUpdate{}, err
	}

	// Check status code
	if resp.StatusCode() != 200 {
		err = errors.New(fmt.Sprintf("Status code != 200 (code %d)", resp.StatusCode()))
		return &db.LaunchUpdate{}, err
	}

	// Unmarshal into a launch update struct
	var update db.LaunchUpdate
	err = json.Unmarshal(resp.Body(), &update)

	if err != nil {
		log.Error().Err(err).Msg("Error unmarshaling JSON")
		return &db.LaunchUpdate{}, err
	}

	return &update, nil
}

// Function that chrono calls when a scheduled API update runs.
func updateWrapper(session *config.Session, scheduleNext bool) {
	log.Debug().Msgf("Running updateWrapper with scheduleNext=%v", scheduleNext)

	// Run updater in a re-try loop
	for i := 0; ; i++ {
		// Check for success
		success := Updater(session, scheduleNext)

		if !success {
			// If updater failed, do exponential back-off
			retryAfter := 60*2 ^ i

			log.Warn().Msgf("Re-try number %d failed, trying again in %d minutes", i, retryAfter)

			// Sleep, continue loop
			time.Sleep(time.Duration(retryAfter) * time.Second)
			continue
		}

		if i > 0 {
			log.Debug().Msgf("Update succeeded after %d attempt(s)", i+1)
		}

		break
	}

	log.Debug().Msgf("updateWrapper finished successfully")
}

// Handles the API request flow, requesting new data and updating the cached and on-disk data.
func Updater(session *config.Session, scheduleNext bool) bool {
	// Create http-client
	client := resty.New()
	client.SetTimeout(time.Duration(1 * time.Minute))
	client.SetHeader("user-agent", "github.com/499602D2/launchbot-go")

	// Do API call
	log.Info().Msg("Running LL2 API updater...")
	update, err := apiCall(client, session.UseDevEndpoint)

	// TODO use api.errors
	if err != nil || len(update.Launches) == 0 {
		apiErrorHandler(err)
		return false
	}

	// Parse any relevant data before dumping to disk
	parseStartTime := time.Now()
	launches, postponedLaunches, err := parseLaunchUpdate(session.Cache, update)

	log.Debug().Msgf("➙ Launch update parsed in %s (%d launches)",
		durafmt.Parse(time.Since(parseStartTime)).LimitFirstN(1), len(update.Launches))

	if err != nil {
		log.Error().Err(err).Msg("➙ Error parsing launch update")
		return false
	}

	// Update hot launch cache
	session.Cache.Update(launches)
	log.Debug().Msg("➙ Hot launch cache updated")

	// Update on-disk database
	err = session.Db.Update(launches, true, true)
	log.Info().Msg("➙ Launch database updated")

	if err != nil {
		log.Error().Err(err).Msg("➙ Error inserting launches to database")
		return false
	}

	// Clean the launch database
	err = session.Db.CleanSlippedLaunches()

	if err != nil {
		log.Error().Err(err).Msg("➙ Error cleaning launch database")
		return false
	}

	// If launches were postponed, notify
	if len(postponedLaunches) != 0 {
		log.Info().Msgf("➙ %d launches were postponed", len(postponedLaunches))

		for launch, postpone := range postponedLaunches {
			// Create sendable for this postpone
			sendable := launch.PostponeNotificationSendable(session.Db, postpone, "tg")

			// Enqueue the postpone sendable
			session.Telegram.Queue.Enqueue(sendable, false)
		}
	} else {
		log.Debug().Msg("➙ No launches were postponed")
	}

	// Save stats
	session.Telegram.Stats.LastApiUpdate = time.Now()

	// Schedule next API update, if configured
	if scheduleNext {
		return Scheduler(session, false, nil)
	}

	return true
}
