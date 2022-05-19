package migration

import (
	"launchbot/db"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type v2User struct {
	Chat                  string `gorm:"primaryKey"`
	SubscribedSince       int64
	MemberCount           int
	TimeZone              string
	TimeZoneStr           string
	CommandPermissions    string
	PostponeNotify        bool
	NotifyTimePref        string
	EnabledNotifications  string
	DisabledNotifications string
}

type v2Stats struct {
	Notifications int64 `gorm:"primaryKey"`
	ApiRequests   int64 `gorm:"primaryKey"`
	DbUpdates     int64
	Commands      int64
	Data          int64
	LastApiUpdate int64
}

func OpenOldDatabase(dbFolder string) *gorm.DB {
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
	dbName := "v2-database.db"
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
	db, err := gorm.Open(sqlite.Open(relDbPath), &gorm.Config{
		CreateBatchSize: 100,
		Logger:          gormZerolog,
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
		return nil
	}

	// Models for auto-migration
	// TODO migrate v2Launch
	users := v2User{}
	stats := v2Stats{}

	// Run auto-migration: creates tables that don't exist and adds missing cols
	err = db.AutoMigrate(&users, &stats)

	if err != nil {
		log.Fatal().Err(err).Msg("Running auto-migration failed")
	}

	return db
}

func MigrateLaunches() {}

func MigrateUsers() {}

func MigrateStatistics() {}

// Implements a migration script from LaunchBot v2 (Python) to LaunchBot v3 (Go)
func RunMigration() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})

	// Open old database
	oldDb := OpenOldDatabase("v2-database.db")

	// Open new database
	newDb := db.Database{Path: "v3-database.db"}
	newDb.Open("v3-database.db")

	// Load all users from v2 database into a slice of v2Users
}
