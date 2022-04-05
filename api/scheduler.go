package api

import (
	"fmt"
	"launchbot/launch"
)

// Schedule next API call
// Schedule notifications

/* Schedules the next notification */
func scheduleNotifications() {
	fmt.Printf("Pseudo notification enqueued at api.scheduler")
}

func scheduleNextUpdate(*launch.LaunchCache) {
	// Check if we have pending notifications
	scheduleNotifications()
}
