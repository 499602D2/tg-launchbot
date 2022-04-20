package db

import (
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Database struct {
	Conn               *gorm.DB
	LaunchTableUpdated time.Time
	Size               float32
	Owner              int64
}

func (db *Database) Open(dbFolder string) bool {
	// Current working directory
	wd, _ := os.Getwd()

	// Verify the path exists
	relDbFolder := filepath.Join(wd, dbFolder)

	// Create folders
	if _, err := os.Stat(relDbFolder); os.IsNotExist(err) {
		log.Info().Msgf("Database folder [%s] does not exist: creating", relDbFolder)
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

		// Create SQLite file
		file, err := os.Create(relDbPath)

		if err != nil {
			log.Fatal().Err(err).Msg("Error creating database file")
		}

		log.Info().Msg("Database file created")
		file.Close()
	}

	// Open DB connection
	log.Info().Msgf("Opening sqlite3 database at %s", relDbPath)

	var err error
	db.Conn, err = gorm.Open(sqlite.Open(relDbPath), &gorm.Config{})

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
		return false
	}

	// Models for auto-migration
	launches := Launches{}
	chats := Chat{}
	stats := Stats{}

	// Run auto-migration: creates tables that don't exist and adds missing cols
	err = db.Conn.AutoMigrate(&launches, &chats, &stats)

	if err != nil {
		log.Fatal().Err(err).Msg("Running auto-migration failed")
	}

	log.Info().Msg("Database ready")

	return true
}

/* Cleans launches from the DB that have slipped away from the request range.
This could be the result of the NET moving to the right, or the launch being
deleted. */
func (db *Database) CleanSlippedLaunches() error {
	// Clean all launches that have launched = 0 and weren't updated in the last update

	/*
		# Select all launches
		cursor.execute(
		'SELECT unique_id FROM launches WHERE launched = 0 AND last_updated < ? AND net_unix > ?',
		(last_update, int(time.time())))

		deleted_launches = set()
		for launch_row in cursor.fetchall():
			deleted_launches.add(launch_row[0])

		# If no rows returned, nothing to do
		if len(deleted_launches) == 0:
			logging.debug('✨ Database already clean: nothing to do!')
			return

		# More than one launch out of range
		logging.info(
		f'✨ Deleting {len(deleted_launches)} launches that have slipped out of range...'
		)

		cursor.execute(
			'DELETE FROM launches WHERE launched = 0 AND last_updated < ? AND net_unix > ?',
			(last_update, int(time.time())))
	*/
	return nil
}

// Check if DB needs to be updated immediately
func (db *Database) RequireImmediateUpdate() bool {
	return true
}
