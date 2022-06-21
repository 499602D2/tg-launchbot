package api

/*
	The updater updates the local database.
*/

import (
	"encoding/json"
	"fmt"
	"launchbot/config"
	"launchbot/db"
	"math"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/hako/durafmt"
	"github.com/rs/zerolog/log"
)

// Performs an LL2 API call
func apiCall(client *resty.Client, useDevEndpoint bool) (*db.LaunchUpdate, error) {
	const (
		apiVersion  = "2.2.0"
		requestPath = "launch/upcoming"
		apiParams   = "mode=detailed&limit=30"
	)

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

	if err != nil {
		log.Error().Err(err).Msg("Error performing GET request")
		return &db.LaunchUpdate{}, err
	}

	// Check status code
	if resp.StatusCode() != 200 {
		err = fmt.Errorf("Status code != 200 (code %d)", resp.StatusCode())
		return &db.LaunchUpdate{}, err
	}

	// Unmarshal into a launch update struct
	var update db.LaunchUpdate
	err = json.Unmarshal(resp.Body(), &update)

	// Init the postponed map of the update
	update.Postponed = make(map[*db.Launch]db.Postpone)

	if err != nil {
		log.Error().Err(err).Msg("Error unmarshaling JSON")
		return &db.LaunchUpdate{}, err
	}

	return &update, nil
}

// Function that chrono calls when a scheduled API update runs.
func updateWrapper(session *config.Session, scheduleNext bool) {
	log.Debug().Msgf("Running updateWrapper with scheduleNext=%v", scheduleNext)

	// Log start-time for failures
	startTime := time.Now()

	// Run updater in a re-try loop
	for i := 1; ; i++ {
		// Check for success
		success := Updater(session, scheduleNext)

		if !success {
			// If updater failed, do exponential back-off
			retryAfter := math.Pow(2.0, float64(i))

			log.Warn().Msgf("Re-try number %d failed, trying again in %.1f seconds", i, retryAfter)

			// Sleep, continue loop
			time.Sleep(time.Duration(retryAfter) * time.Second)
			continue
		}

		if i > 0 {
			log.Debug().Msgf("Update succeeded after %d attempt(s), took %s",
				i, durafmt.Parse(time.Since(startTime)).LimitFirstN(2).String())
		}

		break
	}

	log.Debug().Msgf("updateWrapper finished successfully")
}

// Handles the API request flow, requesting new data and updating the cached and on-disk data.
func Updater(session *config.Session, scheduleNext bool) bool {
	// Create http-client
	client := resty.New()
	client.SetTimeout(time.Duration(30 * time.Second))
	client.SetHeader(
		"user-agent", fmt.Sprintf("%s (telegram @%s)", session.Github, session.Telegram.Username),
	)

	// Do API call
	log.Info().Msg("Running LL2 API updater...")
	update, err := apiCall(client, session.UseDevEndpoint)

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
	session.Cache.UpdateWithNew(launches)
	log.Debug().Msg("➙ Hot launch cache updated")

	// Update on-disk database
	err = session.Db.Update(launches, true, true)
	log.Debug().Msg("➙ Launch database updated")

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

	// Is flushing the user cache safe?
	safeToFlushCache := false

	// If launches were postponed, notify
	if len(postponedLaunches) != 0 {
		log.Info().Msgf("➙ %d launches were postponed", len(postponedLaunches))

		for launch, postpone := range postponedLaunches {
			// Create sendable for this postpone
			sendable := launch.PostponeNotificationSendable(session.Db, postpone, "tg")

			// Enqueue the postpone sendable
			session.Telegram.Enqueue(sendable, false)
		}
	} else {
		log.Debug().Msg("➙ No launches were postponed")
	}

	// Save stats
	session.Telegram.Stats.LastApiUpdate = time.Now()
	session.Telegram.Stats.ApiRequests++

	// Schedule next API update, if configured
	if scheduleNext {
		if len(postponedLaunches) == 0 && !session.Telegram.Spam.NotificationSendUnderway {
			// Flushing cache is safe under these conditions
			safeToFlushCache = true
		}

		return Scheduler(session, false, nil, safeToFlushCache)
	}

	return true
}
