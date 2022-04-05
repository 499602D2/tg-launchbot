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

func apiCall(client *http.Client) (launch.LL2LaunchUpdate, error) {
	const apiVersion = "2.2.0"
	const apiEndpoint = "launch/upcoming"
	const apiParams = "mode=detailed&limit=30"

	// Construct the URL
	url := fmt.Sprintf(
		"https://ll.thespacedevs.com/%s/%s?%s",
		apiVersion, apiEndpoint, apiParams,
	)

	// Do request
	resp, err := client.Get(url)

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
		log.Error().Err(err).Msg("Error unmarshaling JSON!")
		return launch.LL2LaunchUpdate{}, err
	}

	return update, nil
}

func Updater(session *config.Session) bool {
	/*
		Updater handles the update flow, scheduling new updates for when
		they are required.

		Returns:
			bool: true/false, indicating update success
	*/

	// Create http-client
	client := http.Client{
		Timeout: 15 * time.Second,
	}

	// Before doing API call, check if we need to do one (e.g. restart)
	if !session.Db.RequireImmediateUpdate() {
		return true
	}

	// Do API call
	update, err := apiCall(&client)
	fmt.Printf("Got %d launches", update.Count)

	if err != nil {
		fmt.Printf("Error performing API update!")
	}

	// Parse any relevant data before dumping to disk
	launches, err := parseLaunchUpdate(&update)
	fmt.Printf("Launch update parsed!")

	if err != nil {
		fmt.Printf("Error parsing launch update!")
	}

	// Update launch cache (launch.cache)
	session.LaunchCache.Update(launches)
	fmt.Printf("Hot launch cache updated!")

	// Dump to disk
	err = updateLaunchDatabase(launches)
	fmt.Printf("Launch database updated!")

	if err != nil {
		fmt.Printf("Error inserting launches to database")
	}

	// Schedule API update + notifications
	scheduleNextUpdate(session.LaunchCache)

	return true
}
