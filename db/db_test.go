package db

import (
	"fmt"
	"launchbot/users"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func RemoveTestDb(dbFolder string) {
	// Current working directory
	wd, _ := os.Getwd()

	// Verify the path exists
	relDbFolder := filepath.Join(wd, dbFolder)

	// Remove database file
	err := os.Remove(filepath.Join(relDbFolder, "launchbot.db"))

	if err != nil {
		log.Error().Err(err).Msg("Unable to remove database")
	}

	// Remove database folder
	err = os.Remove(relDbFolder)

	if err != nil {
		log.Error().Err(err).Msg("Unable to remove database folder")
	}
}

func TestDatabaseOpen(t *testing.T) {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})

	db := Database{}

	dbFolder := "test"
	success := db.Open(dbFolder)

	if !success {
		log.Error().Msg("Error opening database")
		t.Fail()
	}

	// Toggle to remove the files and folders created
	if false {
		RemoveTestDb(dbFolder)
	}
}

func TestChatMethods(t *testing.T) {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})

	// Open db
	db := Database{}
	dbFolder := "/test"
	db.Open(dbFolder)

	// Add a whole bunch of users, then remove them
	for i := 0; i < 10; i++ {
		chat := users.User{
			Id:                   fmt.Sprint(i),
			Platform:             "tg",
			Locale:               "Europe/Berlin",
			SubscribedNewsletter: true,
		}

		err := db.Conn.Create(&chat).Error

		if err != nil {
			log.Error().Err(err).Msg("Transaction failed")
			t.Fail()
		}

		log.Debug().Msgf("Inserted chat with id=%s", chat.Id)

		// Test removal
		db.RemoveUser(&chat)
	}

	// Test removing chat that doesn't exist
	fauxUser := users.User{
		Id:       "abcd",
		Platform: "tg",
	}

	log.Debug().Msg("Deleting non-existent user")
	db.RemoveUser(&fauxUser)

	// Toggle to remove the files and folders created
	if true {
		RemoveTestDb(dbFolder)
	}

	log.Info().Msg("Done!")
}
