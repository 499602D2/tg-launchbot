package api

import "github.com/rs/zerolog/log"

func apiErrorHandler(err error) bool {
	// TODO notify admin of processing failures
	log.Error().Err(err).Msg("Error in API functions")
	return false
}
