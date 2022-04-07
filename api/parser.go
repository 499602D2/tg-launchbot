package api

import (
	"fmt"
	"launchbot/launch"
	"reflect"
	"time"

	"github.com/rs/zerolog/log"
)

/* Inserts the parsed launches into the database */
func updateLaunchDatabase(launches *[]launch.Launch) error {
	// All fields of a launch.Launch struct
	fields := reflect.VisibleFields(reflect.TypeOf(struct{ launch.Launch }{}))

	for _, l := range *launches {
		// Iterate over keys...? This is one massive insert
		// Use launchbot/db to execute the query; just construct it here?
		// Use a const for launch update insert...?
		// Iterate over the fields of the launch...?

		// Access fields and values with reflect -> construct string
		// FIRST: do a new schema, see what is needed and what is not
		// If field is a sub-struct, do...
		for _, field := range fields {
			fmt.Printf("Key: %s\tType: %s\tValue: %v\n", field.Name, field.Type, reflect.ValueOf(l).FieldByName(field.Name))
		}

		/*
			reflectVal := reflect.ValueOf(l)
			lType := reflectVal.Type()

			for i := 0; i < reflectVal.NumField(); i++ {
				fmt.Printf("Field: %s\tValue: %v\n", lType.Field(i).Name, reflectVal.Field(i).Interface())
			} */

		//fields := ""
		//values := l.FieldValues()
		//query := fmt.Sprintf("INSERT INTO launches (%s) VALUES (%s)", fields, values)
	}

	return nil
}

/* Checks if a launch was postponed */
func getPostponedLaunches(launches *[]launch.Launch) *[]launch.Launch {
	postponedLaunches := []launch.Launch{}

	return &postponedLaunches
}

func netSlipped(cache *launch.LaunchCache, ll2launch *launch.Launch) (bool, int64) {
	// If cache exists, use it
	if cache.Updated != 0 {
		// Find launch
		log.Info().Msg("[netSlipped()] cache exists")

		/* Launch not found in cache, check on disk

		The launch could have e.g. launched between the two checks, and might thus
		have disappeared from the /upcoming endpoint */
	} else {
		// Compare on disk
		log.Info().Msg("[netSlipped()] cache not found, using disk")
	}

	return false, 0
}

/* Parses the LL2 launch update */
func parseLaunchUpdate(cache *launch.LaunchCache, update *launch.LL2LaunchUpdate) (*[]launch.Launch, error) {
	var utcTime time.Time
	var err error

	// Loop over launches and do any required operations
	for i, ll2launch := range update.Launches {
		// Parse the datetime string as RFC3339 into a time.Time object in UTC
		utcTime, err = time.ParseInLocation(time.RFC3339, ll2launch.NET, time.UTC)

		if err != nil {
			log.Error().Err(err).Msg("Error parsing RFC3339 launch time")
		}

		// Convert to unix time, store
		ll2launch.NETUnix = time.Time.Unix(utcTime)

		// If launch slipped, set postponed flag
		postponed, by := netSlipped(cache, &ll2launch)
		if postponed {
			ll2launch.Postponed = true
			ll2launch.PostponedBy = by
		}

		// If reused stage information, parse

		log.Debug().Msgf("[%2d] launch %s processed", i, ll2launch.Slug)
	}

	return &update.Launches, nil
}
