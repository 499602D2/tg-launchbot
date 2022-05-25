package db

import (
	"launchbot/users"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestRecipientLoading(t *testing.T) {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})

	// Open db
	db := Database{}
	dbFolder := "../data"
	db.Open(dbFolder)

	cache := &Cache{
		Database:  &db,
		Launches:  []*Launch{},
		LaunchMap: make(map[string]*Launch),
		Users:     &users.UserCache{},
	}

	db.Cache = cache
	db.Cache.Populate()

	launch, err := db.Cache.FindLaunchById("949421ac-3802-499b-b383-d8274de7e147")

	if err != nil {
		log.Fatal().Err(err).Msg("Loading launch failed")
	}

	user := db.Cache.FindUser("12345", "tg")
	log.Debug().Msgf("User=%s pre-loaded into the cache", user.Id)

	notificationType := "24h"
	platform := "tg"

	recipients := launch.NotificationRecipients(&db, notificationType, platform)
	log.Debug().Msgf("Loaded %d recipients!", len(recipients))
	log.Debug().Msgf("User-cache length: %d", len(cache.Users.InCache))
}
