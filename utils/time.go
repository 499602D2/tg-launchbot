package utils

import (
	"fmt"
	"time"
)

func TimeInUserLocation(refTime int64, userLocation *time.Location, utcOffset string) string {
	// Convert unix time to local time in user's time zone
	userTime := time.Unix(refTime, 0).In(userLocation)

	// Create time string, escape it
	timeString := fmt.Sprintf("%02d:%02d %s",
		userTime.Hour(), userTime.Minute(), utcOffset)

	return timeString
}
