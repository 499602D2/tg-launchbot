package main

import (
	"fmt"
	"launchbot/db"
	"launchbot/stats"
	"launchbot/users"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// v2 database chat rows. Gorm simply pluralizes struct names, so these names
// don't really make any sense.
type chat struct {
	Chat                  string `gorm:"primaryKey"`
	SubscribedSince       int64
	MemberCount           int
	TimeZone              string // not migrated
	TimeZoneStr           string
	CommandPermissions    string // unused
	PostponeNotify        bool   // unused
	NotifyTimePref        string
	EnabledNotifications  string
	DisabledNotifications string
}

// v2 database stats
type stat struct {
	Notifications int64 `gorm:"primaryKey"`
	ApiRequests   int64 `gorm:"primaryKey"`
	DbUpdates     int64
	Commands      int64
	Data          int64
	LastApiUpdate int64
}

// Extended map, as most names are not found in LSPShorthands
var extendedLSPNameMap = map[string]int{
	"Rocket Lab Ltd":                                     147,
	"Antrix Corporation":                                 190,
	"Interstellar Technologies":                          1002,
	"Russian Federal Space Agency (ROSCOSMOS)":           63,
	"Firefly Aerospace":                                  265,
	"Mitsubishi Heavy Industries":                        98,
	"Astra Space":                                        285,
	"China Aerospace Science and Technology Corporation": 88,
	"Northrop Grumman Innovation Systems":                257,
	"Russian Space Forces":                               193,
	"VKS":                                                193,
	"121":                                                121,
	"147":                                                147,
}

func OpenDatabase(folder string, filename string) *gorm.DB {
	// Current working directory
	wd, _ := os.Getwd()

	// Verify the path exists
	relDbFolder := filepath.Join(wd, folder)

	// Create folders
	if _, err := os.Stat(relDbFolder); os.IsNotExist(err) {
		log.Info().Msgf("Database folder [%s] does not exist: creating", relDbFolder)
		err = os.Mkdir(relDbFolder, os.ModePerm)

		if err != nil {
			log.Fatal().Err(err).Msg("Error creating database folder")
		}
	}

	// Database path
	relDbPath := filepath.Join(relDbFolder, filename)

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
	log.Debug().Msgf("Opening sqlite3 database at %s", filepath.Join(folder, filename))

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
	chats := chat{}
	stats := stat{}

	// Run auto-migration: creates tables that don't exist and adds missing cols
	err = db.AutoMigrate(&chats, &stats)

	if err != nil {
		log.Fatal().Err(err).Msg("Running auto-migration failed")
	}

	return db
}

func MigrateStatistics(oldDb *gorm.DB, newDb *db.Database) {
	// Load stats (one row, in one table)
	oldStats := stat{}
	result := oldDb.Model(&stat{}).First(&oldStats)

	if result.Error != nil {
		log.Fatal().Err(result.Error).Msg("Loading statistics failed")
	}

	newStats := stats.Statistics{
		Platform:      "tg",
		Notifications: int(oldStats.Notifications),
		Commands:      0,
		Callbacks:     0,
		V2Commands:    int(oldStats.Commands),
		ApiRequests:   int(oldStats.ApiRequests),
	}

	result = newDb.Conn.Save(&newStats)

	if result.Error != nil {
		log.Fatal().Err(result.Error).Msg("Saving stats to db failed")
	}

	log.Info().Msg("Statistics saved!")
}

// Maps a v2 LSP name to an LSP ID
func MapProviderNameToId(name string) int {
	// ID is integer, LSP is an LSP{}
	for lspID, lsp := range db.LSPShorthands {
		if lsp.Name == name {
			return lspID
		}
	}

	lspId, ok := extendedLSPNameMap[name]

	if !ok {
		return -1
	}

	return lspId
}

func MigrateUsers(oldDb *gorm.DB, newDb *db.Database) {
	// Load all users from v2 database into a slice of v2Users
	var oldUsers []chat

	result := oldDb.Model(&chat{}).Find(&oldUsers)

	// Log results
	if result.Error != nil {
		log.Fatal().Err(result.Error).Msg("Loading users from v2 database failed")
	}

	log.Info().Msgf("Loaded %d users from v2 database", result.RowsAffected)

	// Create a slice of v3 users
	var newUsers []*users.User

	// Loop over all the old, v2 users we loaded
	for _, user := range oldUsers {
		newUser := users.User{
			Id:       user.Chat,
			Platform: "tg",
			Locale:   user.TimeZoneStr,
		}

		// Init user-stats
		newUser.Stats = stats.User{
			SubscribedSince: user.SubscribedSince,
			MemberCount:     user.MemberCount,
		}

		// Parse enabled- and disabled notifications
		enabled := strings.Split(user.EnabledNotifications, ",")
		disabled := strings.Split(user.DisabledNotifications, ",")

		doNotLog := map[string]bool{
			"Sea Launch":                    true,
			"International Launch Services": true,
			"Starsem SA":                    true,
			"Land Launch":                   true,
			"Eurockot":                      true,
			"ISC Kosmotras":                 true,
		}

		if strings.Contains(user.EnabledNotifications, "All") {
			// All launches enabled, parse disabled launches (enabled ones don't matter)
			newUser.SubscribedAll = true

			v3Disabled := []string{}
			for _, lspName := range disabled {
				if lspName == "" {
					continue
				}

				lspId := MapProviderNameToId(lspName)

				if lspId != -1 {
					v3Disabled = append(v3Disabled, fmt.Sprint(MapProviderNameToId(lspName)))
				} else {
					if _, ok := doNotLog[lspName]; !ok {
						log.Debug().Msgf("Not entering lsp_name=[%s] into database (All=true)", lspName)
					}
				}
			}

			// Save disabled IDs
			newUser.UnsubscribedFrom = strings.Join(v3Disabled, ",")
		} else {
			// All-flag not set, parse enabled launches only (disabled launches don't matter)
			v3Enabled := []string{}
			for _, lspName := range enabled {
				if lspName == "" {
					continue
				}

				lspId := MapProviderNameToId(lspName)

				if lspId != -1 {
					v3Enabled = append(v3Enabled, fmt.Sprint(MapProviderNameToId(lspName)))
				} else {
					if _, ok := doNotLog[lspName]; !ok {
						log.Debug().Msgf("Not entering lsp_name=[%s] into database (All=false)", lspName)
					}
				}
			}

			// Save enabled IDs
			newUser.SubscribedTo = strings.Join(v3Enabled, ",")
		}

		// Parse notification time preferences
		binaryStates := strings.Split(user.NotifyTimePref, ",")

		// 24h -> 12h -> 1h -> 5 min (1,1,1,1)
		newUser.Enabled24h = (binaryStates[0] == "1")
		newUser.Enabled12h = (binaryStates[1] == "1")
		newUser.Enabled1h = (binaryStates[2] == "1")
		newUser.Enabled5min = (binaryStates[3] == "1")

		// Log any fields we will not save
		if user.TimeZone != "" {
			log.Warn().Msgf("Not saving time zone (=%s) for user=%s", user.TimeZone, user.Chat)
		}

		newUsers = append(newUsers, &newUser)
	}

	// Save to db as a batch
	result = newDb.Conn.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&newUsers)

	if result.Error != nil {
		log.Fatal().Err(result.Error).Msg("Batch insert of users failed")
	}

	log.Info().Msgf("Users saved! Total migrated: %d", len(newUsers))
}

// Implements a migration script from LaunchBot v2 (Python) to LaunchBot v3 (Go)
func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})

	// Open or create databases
	oldDb := OpenDatabase("", "v2-database.db")

	// Open new database
	newDb := db.Database{}
	newDb.Open("")

	// Migrate users
	MigrateUsers(oldDb, &newDb)

	// Migrate stats
	MigrateStatistics(oldDb, &newDb)
}
