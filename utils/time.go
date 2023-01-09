package utils

import (
	"fmt"
	"math"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/dustin/go-humanize/english"
)

// Returns a time-string in user's location, e.g. "14:00 UTC+5"
func TimeInUserLocation(refTime int64, userLocation *time.Location, utcOffset string) string {
	// Convert unix time to local time in user's time zone
	userTime := time.Unix(refTime, 0).In(userLocation)

	// Create time string, escape it
	timeString := fmt.Sprintf("%02d:%02d %s",
		userTime.Hour(), userTime.Minute(), utcOffset)

	return timeString
}

// Returns the date in user's location, e.g. "May 5th"
func DateInUserLocation(refTime int64, userLocation *time.Location) string {
	userLaunchTime := time.Unix(refTime, 0).In(userLocation)
	return fmt.Sprintf("%s %s", userLaunchTime.Month().String(), humanize.Ordinal(userLaunchTime.Day()))
}

// Return a user-friendly ETA string, e.g. "today", "tomorrow", or "in 5 days"
func FriendlyETA(userNow time.Time, eta time.Duration) string {
	// See if eta + userNow is still the same day
	if userNow.Add(eta).Day() == userNow.Day() {
		// Same day: launch is today
		return "today"
	}

	// Remove seconds that are not contributing to a whole day.
	// As in, TTL might be 1.25 days: extract the .25 days
	mod := int64(eta.Seconds()) % (24 * 3600)

	var daysUntil float64

	// If, even after adding the remainder, the day is still today, calculating days is simple
	if userNow.Add(time.Second*time.Duration(mod)).Day() == userNow.Day() {
		daysUntil = eta.Hours() / 24
	} else {
		// If the remained, e.g. .25 days, causes us to jump over to tomorrow, add a +1 to the days
		daysUntil = eta.Hours()/24 + 1
	}

	if daysUntil <= float64(1) {
		// The case of the date being today has already been caught, therefore it's tomorrow
		return "tomorrow"
	}

	// Otherwise, just count the days
	return fmt.Sprintf("in %s", english.Plural(int(math.Ceil(daysUntil)), "day", "days"))
}
