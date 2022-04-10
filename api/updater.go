package api

/*
	The updater updates the local database.
*/

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"launchbot/config"
	"launchbot/ll2"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

/* Performs an LL2 API call */
func apiCall(client *http.Client) (ll2.LaunchUpdate, error) {
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
		return ll2.LaunchUpdate{}, err
	}

	// Add user-agent headers, because we're nice TODO: pull from config
	request.Header.Add("user-agent", "github.com/499602D2/tg-launchbot")

	// Do request
	resp, err := client.Do(request)

	if err != nil {
		log.Error().Err(err).Msg("Error performing GET request")
		return ll2.LaunchUpdate{}, err
	}

	// Read bytes from returned data
	defer resp.Body.Close()
	bytes, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		log.Error().Err(err).Msg("Error parsing resp body")
		return ll2.LaunchUpdate{}, err
	}

	// Unmarshal into a launch update struct
	var update ll2.LaunchUpdate
	err = json.Unmarshal(bytes, &update)

	if err != nil {
		log.Error().Err(err).Msg("Error unmarshaling JSON")
		return ll2.LaunchUpdate{}, err
	}

	// Set update count manually
	update.Count = len(update.Launches)

	return update, nil
}

/* Updater handles the update flow, scheduling new updates for when
they are required.

Flow:
1. Verify the database needs to be updated
2. Perform the API call
3. Parse the data we got
4. Update hot cache
5. Update on-disk database
6. Clean the on-disk database
7. Get launches that were postponed
	7.1. Notify of postponed launches
8. Schedule next API update and notifications

Returns:
	bool: true/false, indicating update success */
func Updater(session *config.Session) bool {
	log.Info().Msg("Starting LL2 API updater...")

	// Before doing API call, check if we need to do one (e.g. restart)
	if !session.Db.RequireImmediateUpdate() {
		log.Info().Msg("Database does not require an immediate update: loading cache")

		// TODO: init cache if db doe not require an update
		return true
	}

	// Create http-client
	client := http.Client{
		Timeout: 15 * time.Second,
	}

	// Do API call
	update, err := apiCall(&client)
	log.Info().Msgf("Got %d launches", update.Count)

	if err != nil {
		log.Error().Msg("Error performing API update")
		return false
	}

	// Parse any relevant data before dumping to disk
	launches, err := parseLaunchUpdate(session.LaunchCache, &update)
	log.Info().Msg("Launch update parsed")

	if err != nil {
		log.Error().Err(err).Msg("Error parsing launch update")
		return false
	}

	// Update launch cache (launch.cache)
	session.LaunchCache.Update(launches)
	log.Info().Msg("Hot launch cache updated")

	// Dump launches to disk
	err = updateLaunchDatabase(launches)
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
		log.Info().Msgf("%d launches were postponed", len(postponedLaunches))
	} else {
		log.Info().Msg("No launches were postponed")
	}

	// Schedule API update + notifications
	scheduleNextUpdate(session.LaunchCache)

	return true
}
