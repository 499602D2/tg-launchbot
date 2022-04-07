package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"
)

type Database struct {
	Connection *sql.DB
	Updated    time.Time
	Size       float32
}

func (db *Database) Open(dbFolder string) bool {
	var err error

	// Current working directory
	wd, _ := os.Getwd()

	// Verify the path exists
	relDbFolder := filepath.Join(wd, dbFolder)
	if _, err := os.Stat(relDbFolder); os.IsNotExist(err) {
		log.Info().Msgf("Database folder does not exist: creating", relDbFolder)
		err = os.Mkdir(relDbFolder, os.ModePerm)

		if err != nil {
			log.Fatal().Err(err).Msg("Error creating database folder")
		}
	}

	// Database path
	dbName := "launchbot.db"
	relDbPath := filepath.Join(relDbFolder, dbName)

	// Verify the database file exists
	if _, err := os.Stat(relDbPath); os.IsNotExist(err) {
		log.Info().Msg("Database file does not exist: creating")

		file, err := os.Create(relDbPath) // Create SQLite file
		if err != nil {
			log.Fatal().Err(err).Msg("Error creating database file")
		}

		log.Info().Msg("Database file created")
		file.Close()
	}

	// Open DB connection
	log.Info().Msgf("Opening sqlite3 database at %s", filepath.Join(dbFolder, dbName))
	db.Connection, err = sql.Open("sqlite3", relDbPath)

	if err != nil {
		log.Fatal().Err(err).Msg("Error opening database")
		return false
	}

	// Verify tables exist
	db.verifyTablesExist()
	log.Info().Msg("Database ready")

	return true
}

/* Quick method to close the database */
func (db *Database) Close() {
	err := db.Connection.Close()
	if err != nil {
		log.Error().Err(err).Msg("Error closing database")
	}
}

/* Function checks the time the database was last updated, and checks if
an update should be immediately applied following e.g. a restart. */
func (db *Database) RequireImmediateUpdate() bool {
	return true
}
