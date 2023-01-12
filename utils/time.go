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
	if userNow.Add(eta).Day() == userNow.Day() {
		// Same day: launch is today
		return "today"
	}

	// Extract seconds that are not contributing to a whole day in the ETA.
	// Example: ETA might be 1.25 days -> extract the .25 days.
	remainder := int64(eta.Seconds()) % (24 * 3600)

	// Seconds left in today: 24 hours, minus time elapsed
	secondsRemainingToday := 24*3600 - (userNow.Hour()*3600 + userNow.Minute()*60 + userNow.Second())

	// If ETA is within the range of today's remaining seconds + tomorrow's
	// seconds, the ETA is tomorrow
	if int(eta.Seconds()) <= 24*3600+secondsRemainingToday {
		return "tomorrow"
	}

	// Floating point whole days until ETA
	daysUntil := (eta.Seconds() - float64(secondsRemainingToday) - float64(remainder)) / (24 * 3600)

	if userNow.Add(time.Second*time.Duration(remainder)).Day() != userNow.Day() {
		// If remainder is enough to kick us off into the next day, account for it
		daysUntil += 1
	}

	return fmt.Sprintf("in %s", english.Plural(int(math.Ceil(daysUntil)), "day", "days"))
}
