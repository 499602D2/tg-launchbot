package api

import (
	"launchbot/db"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
)

// Finds the highest-priority link for the launch
func GetHighestPriorityVideoLink(links *[]db.ContentURL) db.ContentURL {

	return db.ContentURL{}
}

/* Inserts the parsed launches into the database. */
func updateLaunchDatabase(db *db.Database, launches []*db.Launch) error {
	// All fields of a launch.Launch struct
	//fields := reflect.VisibleFields(reflect.TypeOf(struct{ ll2.Launch }{}))

	//for _, launch := range launches {
	// Iterate over keys...? This is one massive insert
	// Use launchbot/db to execute the query; just construct it here?
	// Use a const for launch update insert...?
	// Iterate over the fields of the launch...?

	// Access fields and values with reflect -> construct string
	// FIRST: do a new schema, see what is needed and what is not
	// If field is a sub-struct, do...

	//for _, field := range fields {
	//	fmt.Printf("Key: %s\tType: %s\tValue: %v\n", field.Name, field.Type, reflect.ValueOf(*launch).FieldByName(field.Name))
	//}

	/*
		reflectVal := reflect.ValueOf(l)
		lType := reflectVal.Type()

		for i := 0; i < reflectVal.NumField(); i++ {
			fmt.Printf("Field: %s\tValue: %v\n", lType.Field(i).Name, reflectVal.Field(i).Interface())
		} */

	//fields := ""
	//values := l.FieldValues()
	//query := fmt.Sprintf("INSERT INTO launches (%s) VALUES (%s)", fields, values)
	//}

	db.LaunchTableUpdated = time.Now()

	return nil
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

		// If launch slipped, set postponed flag
		// TODO implement
		postponed, by := netSlipped(cache, launch)
		log.Info().Msgf("Launch postponed (%s): %s", postponed, by)

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
