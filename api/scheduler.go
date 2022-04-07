package api

import (
	"launchbot/launch"

	"github.com/rs/zerolog/log"
)

// Schedule next API call
// Schedule notifications

/* Schedules the next notification */
func scheduleNotifications() {
	log.Info().Msg("Pseudo notification enqueued at api.scheduler")
}

func scheduleNextUpdate(*launch.LaunchCache) {
	// Check if we have pending notifications
	scheduleNotifications()
}
