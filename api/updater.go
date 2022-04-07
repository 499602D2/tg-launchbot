package api

/*
	The updater updates the local database.
*/

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"launchbot/config"
	"launchbot/launch"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

/* Performs an LL2 API call */
func apiCall(client *http.Client) (launch.LL2LaunchUpdate, error) {
	const apiVersion = "2.2.0"
	const apiEndpoint = "launch/upcoming"
	const apiParams = "mode=detailed&limit=30"

	/*
		Prod URL: https://ll.thespacedevs.com
		Dev URL: https://lldev.thespacedevs.com
	*/
	// Construct the URL
	url := fmt.Sprintf(
		"https://lldev.thespacedevs.com/%s/%s?%s", apiVersion, apiEndpoint, apiParams,
	)

	// Create request
	request, err := http.NewRequest("GET", url, nil)

	if err != nil {
		log.Error().Err(err).Msg("Error creating request")
		return launch.LL2LaunchUpdate{}, err
	}

	// Add user-agent headers, because we're nice TODO: pull from config
	request.Header.Add("user-agent", "github.com/499602D2/tg-launchbot")

	// Do request
	resp, err := client.Do(request)

	if err != nil {
		log.Error().Err(err).Msg("Error performing GET request")
		return launch.LL2LaunchUpdate{}, err
	}

	// Read bytes from returned data
	defer resp.Body.Close()
	bytes, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		log.Error().Err(err).Msg("Error parsing resp body")
		return launch.LL2LaunchUpdate{}, err
	}

	// Unmarshal into a launch update struct
	var update launch.LL2LaunchUpdate
	err = json.Unmarshal(bytes, &update)

	if err != nil {
		log.Error().Err(err).Msg("Error unmarshaling JSON")
		return launch.LL2LaunchUpdate{}, err
	}

	// Set update count manually
	update.Count = len(update.Launches)

	return update, nil
}

/*
	Updater handles the update flow, scheduling new updates for when
	they are required.

	Returns:
		bool: true/false, indicating update success
*/
func Updater(session *config.Session) bool {
	log.Info().Msg("Starting LL2 API updater...")

	// Create http-client
	client := http.Client{
		Timeout: 15 * time.Second,
	}

	// Before doing API call, check if we need to do one (e.g. restart)
	if !session.Db.RequireImmediateUpdate() {
		log.Info().Msg("Database does not require an immediate update: loading cache")
		log.Warn().Msg("TODO: init cache")
		return true
	}

	// Do API call
	update, err := apiCall(&client)
	log.Info().Msgf("Got %d launches", update.Count)

	if err != nil {
		log.Error().Msg("Error performing API update")
	}

	// Parse any relevant data before dumping to disk
	launches, err := parseLaunchUpdate(session.LaunchCache, &update)
	log.Info().Msg("Launch update parsed")

	if err != nil {
		log.Error().Err(err).Msg("Error parsing launch update")
	}

	// Update launch cache (launch.cache)
	session.LaunchCache.Update(launches)
	log.Info().Msg("Hot launch cache updated")

	// Dump launches to disk
	err = updateLaunchDatabase(launches)
	log.Info().Msg("Launch database updated")

	if err != nil {
		log.Error().Err(err).Msg("Error inserting launches to database")
	}

	// Clean the launch database
	err = session.Db.CleanSlippedLaunches()

	if err != nil {
		log.Error().Err(err).Msg("Error cleaning launch database")
	}

	// Parse for postponed launches, now that DB has been cleaned
	postponedLaunches := getPostponedLaunches(launches)
	if len(*postponedLaunches) != 0 {
		log.Info().Msgf("%d launches were postponed", len(*postponedLaunches))
	} else {
		log.Info().Msg("No launches were postponed")
	}

	// Schedule API update + notifications
	scheduleNextUpdate(session.LaunchCache)

	return true
}
