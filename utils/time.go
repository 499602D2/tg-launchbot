package utils

import (
	"fmt"
	"time"

	"github.com/dustin/go-humanize/english"
)

func TimeInUserLocation(refTime int64, userLocation *time.Location, utcOffset string) string {
	// Convert unix time to local time in user's time zone
	userTime := time.Unix(refTime, 0).In(userLocation)

	// Create time string, escape it
	timeString := fmt.Sprintf("%02d:%02d %s",
		userTime.Hour(), userTime.Minute(), utcOffset)

	return timeString
}

func FriendlyETA(userNow time.Time, eta time.Duration) string {
	// ETA string, e.g. "today", "tomorrow", or "in 5 days"
	var friendlyETA string

	// See if eta + userNow is still the same day
	if userNow.Add(eta).Day() == userNow.Day() {
		// Same day: launch is today
		friendlyETA = "today"
	} else {
		var daysUntil float64

		// Remove seconds that are not contributing to a whole day.
		// As in, TTL might be 1.25 days: extract the .25 days
		mod := int64(eta.Seconds()) % (24 * 3600)

		// If, even after adding the remainder, the day is still today, calculating days is simple
		if time.Now().Add(time.Second*time.Duration(mod)).Day() == time.Now().Day() {
			daysUntil = eta.Hours() / 24
		} else {
			// If the remained, e.g. .25 days, causes us to jump over to tomorrow, add a +1 to the days
			daysUntil = eta.Hours()/24 + 1
		}

		if daysUntil < 2.0 {
			// The case of the date being today has already been caught, therefore it's tomorrow
			friendlyETA = "tomorrow"
		} else {
			// Otherwise, just count the days
			friendlyETA = fmt.Sprintf("in %s", english.Plural(int(daysUntil), "day", "days"))
		}
	}

	return friendlyETA
}
