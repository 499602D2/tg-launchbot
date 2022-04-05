package db

import (
	"database/sql"
	"time"

	"github.com/rs/zerolog/log"
)

type Database struct {
	Connection *sql.DB
	Updated    time.Time
	Size       float32
}

func (db *Database) Open(path string) bool {
	var err error
	db.Connection, err = sql.Open("sqlite3", path)

	if err != nil {
		log.Error().Err(err).Msg("Error opening database!")
		return false
	}

	log.Info().Msg("Database opened!")

	// Verify tables exist
	db.verifyTablesExist()

	return true
}

func (db *Database) Close() {
	err := db.Connection.Close()
	if err != nil {
		log.Error().Err(err).Msg("Error closing database!")
	}
}

func (db *Database) RequireImmediateUpdate() bool {
	return false
}
