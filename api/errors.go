package api

import "github.com/rs/zerolog/log"

func apiErrorHandler(err error) bool {
	log.Error().Err(err).Msg("Error in API functions")
	return false
}
