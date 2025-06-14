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

	// Insert a launch and user manually for the test
	launch := &Launch{
		Id:             "test-launch",
		Slug:           "test-launch",
		Name:           "Test Launch",
		LaunchProvider: LaunchProvider{Id: 123, Name: "Provider"},
		Status:         LaunchStatus{Abbrev: "Go"},
		NETUnix:        time.Now().Add(time.Hour).Unix(),
	}

	if err := db.Update([]*Launch{launch}, true, false); err != nil {
		t.Fatalf("failed to insert launch: %v", err)
	}

	cache.UpdateWithNew([]*Launch{launch})

	user := &users.User{
		Id:              "12345",
		Platform:        "tg",
		SubscribedTo:    "123",
		Enabled24h:      true,
		Enabled12h:      false,
		Enabled1h:       false,
		Enabled5min:     false,
		EnabledPostpone: true,
	}

	db.SaveUser(user)

	user = db.Cache.FindUser("12345", "tg")
	log.Debug().Msgf("User=%s pre-loaded into the cache", user.Id)

	notificationType := "24h"
	platform := "tg"

	recipients := launch.NotificationRecipients(&db, notificationType, platform)
	log.Debug().Msgf("Loaded %d recipients!", len(recipients))
	log.Debug().Msgf("User-cache length: %d", len(cache.Users.InCache))
}
