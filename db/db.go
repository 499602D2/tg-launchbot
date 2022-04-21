package db

import (
	"launchbot/users"
	"os"
	"path/filepath"
	"time"

	"launchbot/stats"

	"github.com/rs/zerolog/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Database struct {
	Conn        *gorm.DB
	LastUpdated time.Time // Last time the Launches-table was updated
	Size        float32   // Db size in megabytes
	Owner       int64     // Telegram admin ID
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
	log.Debug().Msgf("Opening sqlite3 database at %s", relDbPath)

	gormZerolog := logger.New(
		&log.Logger, // IO.writer
		logger.Config{
			SlowThreshold:             time.Second, // Slow SQL threshold
			LogLevel:                  logger.Warn, // Log level
			IgnoreRecordNotFoundError: true,        // Ignore ErrRecordNotFound error for logger
			Colorful:                  false,       // Disable color
		},
	)

	// Open connection
	var err error
	db.Conn, err = gorm.Open(sqlite.Open(relDbPath), &gorm.Config{
		CreateBatchSize: 100,
		Logger:          gormZerolog,
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
		return false
	}

	// Models for auto-migration
	launches := Launch{}
	users := users.User{}
	stats := stats.Statistics{}

	// Run auto-migration: creates tables that don't exist and adds missing cols
	err = db.Conn.AutoMigrate(&launches, &users, &stats)

	if err != nil {
		log.Fatal().Err(err).Msg("Running auto-migration failed")
	}

	log.Info().Msg("Database ready")

	return true
}

// Update database with updated launch data
func (db *Database) Update(launches []*Launch) error {
	// Keep track of update time
	updated := time.Now()

	for _, launch := range launches {
		// Check if launch exists in database
		// https://gorm.io/docs/query.html#Retrieving-objects-with-primary-key
		dummyLaunch := Launch{}
		result := db.Conn.First(&dummyLaunch, "Id = ?", launch.Id)

		// Set update time
		launch.ApiUpdate = updated

		switch result.Error {
		case nil:
			break
		case gorm.ErrRecordNotFound:
			// Record doesn't exist: insert as new
			result = db.Conn.Create(launch)
		default:
			log.Error().Err(result.Error).Msg("Error running db.First()")
			continue
		}

		if result.RowsAffected != 0 {
			// Row exists: update
			result = db.Conn.Save(launch)
		}

		if result.Error != nil {
			log.Error().Err(result.Error).Msgf("Database row creation failed for id=%s",
				launch.Id,
			)
		}
	}

	// if tx.Error != nil {
	// 	log.Error().Err(tx.Error).Msg("Batch transaction failed")
	// 	log.Fatal().Msg("Exiting...")
	// }

	db.LastUpdated = updated

	return nil
}

// Cleans launches from the DB that have slipped away from the request range.
// This could be the result of the NET moving to the right, or the launch being
// deleted.
func (db *Database) CleanSlippedLaunches() error {
	// Dummy launch from grom
	launch := Launch{}
	nowUnix := time.Now().Unix()

	// Find all launches that have launched=0, and weren't updated in the last update
	result := db.Conn.Where(
		"launched = ? AND updated_at < ? AND net_unix > ?",
		false, db.LastUpdated, nowUnix,
	).Find(&launch)

	if result.RowsAffected == 0 {
		log.Info().Msg("Database clean: nothing to do")
		return nil
	}

	log.Info().Msgf("Deleting %d launches that have slipped out of range", result.RowsAffected)
	db.Conn.Where("launched = ? AND updated_at < ? AND net_unix > ?",
		false, db.LastUpdated, nowUnix,
	).Delete(&launch)

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
	log.Info().Msg("Database cleaned")
	return nil
}

// Check if DB needs to be updated immediately
func (db *Database) RequiresImmediateUpdate() bool {
	// Pull largest LastUpdated value from the database
	dest := Launch{}
	result := db.Conn.Limit(1).Order("api_update desc, id").Find(&dest)

	if result.RowsAffected == 0 {
		log.Info().Msg("Tried to pull last update, got nothing: updating now")
		return true
	}

	// Set database's field
	db.LastUpdated = dest.ApiUpdate

	// If more than an hour since last update, update now
	if time.Since(db.LastUpdated) > time.Hour {
		log.Info().Msg("More than an hour since last API update: updating now")
		return true
	}

	log.Info().Msgf("%.0f minutes since last API update, not updating",
		time.Since(db.LastUpdated).Minutes(),
	)

	return false
}
