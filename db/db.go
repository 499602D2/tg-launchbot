package db

import (
	"launchbot/users"
	"os"
	"path/filepath"
	"sync"
	"time"

	"launchbot/stats"

	"github.com/hako/durafmt"
	"github.com/rs/zerolog/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type Database struct {
	Conn        *gorm.DB
	Cache       *Cache
	LastUpdated time.Time // Last time the Launches-table was updated
	Size        int64     // Db size in bytes
	Path        string    // Path on disk
	Owner       int64     // Telegram admin ID
	Subscribers int64
	Mutex       sync.Mutex
}

func (db *Database) SetSize() {
	fileInfo, err := os.Stat(db.Path)

	if err != nil {
		log.Error().Err(err).Msg("Getting database file stats failed")
		return
	}

	// Set the size
	db.Size = fileInfo.Size()
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
	log.Debug().Msgf("Opening sqlite3 database at %s", filepath.Join(dbFolder, dbName))

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

	// Set size
	db.Path = relDbPath
	db.SetSize()

	log.Info().Msg("Database ready")
	return true
}

// Update database with updated launch data
func (db *Database) Update(launches []*Launch, apiUpdate bool, useCacheNotifStates bool) error {
	// Keep track of update time
	updated := time.Now()

	for _, launch := range launches {
		// Set time of API update, if this is one
		if apiUpdate {
			launch.ApiUpdate = updated
		}

		if useCacheNotifStates {
			// Store status of notification sends
			cacheLaunch, ok := db.Cache.LaunchMap[launch.Id]

			if ok {
				// Copy notification states if old launch exists
				launch.NotificationState = cacheLaunch.NotificationState
			} else {
				// If the launch is not cached, check if launch exists on the disk
				dbLaunch, err := db.Cache.FindLaunchById(launch.Id)

				if err != nil {
					// If launch does not exist on the disk, initialize the states (probably a new launch)
					log.Error().Err(err).Msgf("Unable to find launch=%s from disk when searching for notif states", launch.Slug)
					launch.NotificationState.UpdateMap(launch)
					continue
				}

				// If states exist, use the on-disk states
				launch.NotificationState = dbLaunch.NotificationState
				launch.SentNotificationIds = dbLaunch.SentNotificationIds

				log.Debug().Msgf("Successfully utilized on-db launch's notification states on update (id=%s)", launch.Slug)
			}
		}
	}

	// Do a single batch upsert (Update all columns, except primary keys, to new value on conflict)
	// https://gorm.io/docs/create.html#Upsert-x2F-On-Conflict
	result := db.Conn.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&launches)

	if result.Error != nil {
		log.Error().Err(result.Error).Msg("Batch insert failed")
		return result.Error
	}

	// Store LastUpdated value in the database struct
	db.LastUpdated = updated

	// Reload size
	db.SetSize()

	return nil
}

// Cleans launches from the DB that have slipped away from the request range.
// This could be the result of the NET moving to the right, or the launch being
// deleted.
func (db *Database) CleanSlippedLaunches() error {
	// Dummy launch from grom
	nowUnix := time.Now().Unix()

	// Find all launches that have launched=0, and weren't updated in the last update
	result := db.Conn.Unscoped().Where(
		"launched = ? AND updated_at < ? AND net_unix > ?", 0, db.LastUpdated, nowUnix,
	).Delete(&Launch{})

	if result.RowsAffected == 0 {
		log.Info().Msg("Database clean: nothing to do")
	} else {
		log.Info().Msgf("Deleted %d launch(es) that have slipped out of range", result.RowsAffected)
	}

	return nil
}

// Check if DB needs to be updated immediately
func (db *Database) RequiresImmediateUpdate(untilNextUpdate time.Duration) (bool, time.Duration) {
	// Pull largest LastUpdated value from the database
	dest := Launch{}
	result := db.Conn.Limit(1).Order("api_update desc, id").Find(&dest)

	if result.RowsAffected == 0 {
		log.Info().Msg("Tried to pull last update, got nothing: updating now")
		return true, time.Second * 0
	}

	// Set database's field
	db.LastUpdated = dest.ApiUpdate

	// If database is outdated, update now
	if time.Since(db.LastUpdated) > untilNextUpdate {
		log.Info().Msg("Database outdated: updating now...")
		return true, time.Since(db.LastUpdated)
	}

	log.Info().Msgf("%s since last API update, not updating", durafmt.Parse(time.Since(db.LastUpdated)).LimitFirstN(2))
	return false, time.Since(db.LastUpdated)
}

func (db *Database) LoadStatisticsFromDisk(platform string) *stats.Statistics {
	stats := stats.Statistics{Platform: "tg"}
	result := db.Conn.First(&stats)

	switch result.Error {
	case nil:
		// No errors: load subscriber count, return loaded stats
		db.Subscribers = db.GetSubscriberCount()
		return &stats
	case gorm.ErrRecordNotFound:
		// Record doesn't exist: insert as new
		log.Info().Msg("Stats not found: creating...")
	default:
		// Other error: log
		log.Fatal().Err(result.Error).Msg("Unexpected error when loading stats from disk")
		return nil
	}

	// Error was caused by the record not existing: create it
	result = db.Conn.Create(&stats)

	if result.Error != nil {
		log.Fatal().Err(result.Error).Msg("Loading statistics from disk failed")
		return nil
	}

	if result.RowsAffected == 0 {
		log.Warn().Msg("Loaded statistics from disk, but zero rows were affected")
	}

	return &stats
}

// Save stats to disk: called regularly, and on program exit
func (db *Database) SaveStatsToDisk(statistics *stats.Statistics) {
	// Do a single batch upsert (Update all columns, except primary keys, to new value on conflict)
	// https://gorm.io/docs/create.html#Upsert-x2F-On-Conflict
	result := db.Conn.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(statistics)

	if result.Error != nil {
		log.Error().Err(result.Error).Msg("Saving stats to disk failed")
	}

	// On save, get subscriber count (so it's refreshed occasionally)
	db.Subscribers = db.GetSubscriberCount()
}

// Load how many users have subscribed to any notifications
func (db *Database) GetSubscriberCount() int64 {
	// Select all chats with any notifications enabled, and at least one notification time enabled
	result := db.Conn.Where(
		"(subscribed_all = ? OR subscribed_to != ?) AND "+
			"(enabled24h != ? OR enabled12h != ? OR enabled1h != ? OR enabled5min != ?)",
		1, "", 0, 0, 0, 0).Find(&[]users.User{})

	return result.RowsAffected
}
