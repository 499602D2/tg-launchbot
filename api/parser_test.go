package api

import (
	"launchbot/db"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func MoveNET(cache *db.Cache, fakeOrigNet int64, fakeNewNet int64, fakeNotifStates map[string]bool) *db.Launch {
	/* Create a fake launch using the cache (deref)
	This is the "old" launch that the net will be compared against */
	launch := *cache.Launches[0]
	log.Debug().Msgf("Loaded fake launch with slug=%s", launch.Slug)

	// Modify the "original" pre-postpone NET of the launch
	launch.NETUnix = fakeOrigNet

	// Save to cache, update map
	cache.Launches[0] = &launch
	cache.LaunchMap[launch.Id] = &launch

	// Modify notification send-states, update the bool-flags
	launch.NotificationState.Map = fakeNotifStates
	launch.NotificationState.UpdateFlags(&launch)

	// Create the fake "fresh" launch, move NET so that it has been postponed
	freshLaunch := launch
	freshLaunch.NETUnix = fakeNewNet

	return &freshLaunch
}

// Tests if the postpone detection works
func TestPostponeFunctions(t *testing.T) {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})

	// Build the cache so we can compare NETs
	cache := &db.Cache{
		Launches:  []*db.Launch{},
		LaunchMap: make(map[string]*db.Launch)}

	// Open the database
	database := &db.Database{Cache: cache}
	database.Open("../data")
	cache.Database = database

	// Populate cache
	cache.Populate()

	/////////////////////////////////////////////////////////////////////////////
	// Test a postpone out of the [24h...12h] window into the pre-24h window
	log.Info().Msg("postpone out of the [24h...12h] window into the pre-24h window //////////")
	modifiedMap := map[string]bool{"Sent24h": true, "Sent12h": false, "Sent1h": false, "Sent5min": false}
	origNet := time.Now().Unix() + 12*3600
	newNet := time.Now().Unix() + 26*3600
	postponedLaunch := MoveNET(cache, origNet, newNet, modifiedMap)

	// Parse
	wasPostponed, postpone := netParser(cache, postponedLaunch)

	log.Debug().Msgf("Ran netParser, wasPostponed: %v, postpone: %#v",
		wasPostponed, postpone)

	// Generate the sendable (but don't catch it)
	if wasPostponed {
		postponedLaunch.PostponeNotificationSendable(cache.Database, postpone, "tg")
	}

	/////////////////////////////////////////////////////////////////////////////
	// Test a postpone out of the [5min...launch] window into the pre-24h window
	log.Info().Msg("Postpone out of the [5min...launch] window into the pre-24h window //////////")
	modifiedMap = map[string]bool{"Sent24h": true, "Sent12h": true, "Sent1h": true, "Sent5min": true}
	origNet = time.Now().Unix() + 3*60
	newNet = time.Now().Unix() + 26*3600
	postponedLaunch = MoveNET(cache, origNet, newNet, modifiedMap)

	// Parse
	wasPostponed, postpone = netParser(cache, postponedLaunch)

	log.Debug().Msgf("Ran netParser, wasPostponed: %v, postpone: %#v",
		wasPostponed, postpone)

	// Generate the sendable (but don't catch it)
	if wasPostponed {
		postponedLaunch.PostponeNotificationSendable(cache.Database, postpone, "tg")
	}

	/////////////////////////////////////////////////////////////////////////////
	// Test a postpone that does not reset any notification states
	log.Info().Msg("Postpone that does not reset any notification states //////////")
	modifiedMap = map[string]bool{"Sent24h": true, "Sent12h": false, "Sent1h": false, "Sent5min": false}
	origNet = time.Now().Unix() + 16*3600 // Original net 16 hours from now (24h sent)
	newNet = time.Now().Unix() + 22*3600  // New net 22 hours from now (no reset)
	postponedLaunch = MoveNET(cache, origNet, newNet, modifiedMap)

	// Parse
	wasPostponed, postpone = netParser(cache, postponedLaunch)

	log.Debug().Msgf("Ran netParser, wasPostponed: %v, postpone: %#v",
		wasPostponed, postpone)

	// Generate the sendable (but don't catch it)
	if wasPostponed {
		postponedLaunch.PostponeNotificationSendable(cache.Database, postpone, "tg")
	}
}
