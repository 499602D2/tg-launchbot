package api

import (
	"launchbot/ll2"

	"github.com/rs/zerolog/log"
)

// Schedule next API call
// Schedule notifications

/* Schedules the next notification */
func scheduleNotifications() {
	log.Info().Msg("Pseudo notification enqueued at api.scheduler")
}

func scheduleNextUpdate(*ll2.LaunchCache) {
	// Check if we have pending notifications
	scheduleNotifications()
}
