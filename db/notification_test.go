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

func TestKeywordFilteringInNotificationRecipients(t *testing.T) {
	// Test various keyword filtering scenarios
	tests := []struct {
		name               string
		launchName         string
		vehicleName        string
		missionName        string
		user               users.User
		shouldReceive      bool
	}{
		{
			name:          "User with Starlink blocked should not receive Starlink launch",
			launchName:    "Starlink Group 6-23",
			vehicleName:   "Falcon 9",
			missionName:   "Starlink Communications",
			user: users.User{
				Id:              "test1",
				Platform:        "tg",
				Enabled24h:      true,
				SubscribedAll:   true,
				BlockedKeywords: "Starlink",
			},
			shouldReceive: false,
		},
		{
			name:          "User without Starlink blocked should receive Starlink launch",
			launchName:    "Starlink Group 6-23",
			vehicleName:   "Falcon 9",
			missionName:   "Starlink Communications",
			user: users.User{
				Id:              "test2",
				Platform:        "tg",
				Enabled24h:      true,
				SubscribedAll:   true,
				BlockedKeywords: "OneWeb",
			},
			shouldReceive: true,
		},
		{
			name:          "User with Falcon allowed should receive Falcon launch (overrides provider)",
			launchName:    "CRS-29",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Resupply",
			user: users.User{
				Id:              "test3",
				Platform:        "tg",
				Enabled24h:      true,
				SubscribedAll:   true,
				AllowedKeywords: "Falcon",
			},
			shouldReceive: true,
		},
		{
			name:          "User without matching allowed keywords follows provider subscription",
			launchName:    "USSF-51",
			vehicleName:   "Atlas V",
			missionName:   "Military",
			user: users.User{
				Id:              "test4",
				Platform:        "tg",
				Enabled24h:      true,
				SubscribedAll:   true,
				AllowedKeywords: "Falcon,Dragon",
			},
			shouldReceive: true, // User is subscribed to all, so they receive it
		},
		{
			name:          "Blocked keywords take precedence over allowed keywords",
			launchName:    "Starlink Group 6-23",
			vehicleName:   "Falcon 9",
			missionName:   "Starlink Communications",
			user: users.User{
				Id:              "test5",
				Platform:        "tg",
				Enabled24h:      true,
				SubscribedAll:   true,
				AllowedKeywords: "Falcon",
				BlockedKeywords: "Starlink",
			},
			shouldReceive: false,
		},
		{
			name:          "Case insensitive keyword matching",
			launchName:    "STARLINK Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			user: users.User{
				Id:              "test6",
				Platform:        "tg",
				Enabled24h:      true,
				SubscribedAll:   true,
				BlockedKeywords: "starlink",
			},
			shouldReceive: false,
		},
		{
			name:          "Partial keyword matching",
			launchName:    "Starship Test Flight",
			vehicleName:   "Starship",
			missionName:   "Test",
			user: users.User{
				Id:              "test7",
				Platform:        "tg",
				Enabled24h:      true,
				SubscribedAll:   true,
				AllowedKeywords: "Star",
			},
			shouldReceive: true,
		},
		{
			name:          "Multiple keywords with comma separation",
			launchName:    "OneWeb Launch 17",
			vehicleName:   "Soyuz",
			missionName:   "Communications",
			user: users.User{
				Id:              "test8",
				Platform:        "tg",
				Enabled24h:      true,
				SubscribedAll:   true,
				BlockedKeywords: "Starlink,OneWeb,Iridium",
			},
			shouldReceive: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test using ShouldReceiveLaunch which now incorporates keyword filtering
			// We'll use a dummy launch ID and provider ID for this test
			result := tt.user.ShouldReceiveLaunch("test-launch-id", 1, tt.launchName, tt.vehicleName, tt.missionName)
			if result != tt.shouldReceive {
				t.Errorf("ShouldReceiveLaunch: expected %v, got %v", tt.shouldReceive, result)
			}
		})
	}
}
