package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"launchbot/bots"
	"launchbot/bots/discord"
	"launchbot/bots/telegram"
	"launchbot/db"
	"launchbot/users"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/rs/zerolog/log"
)

// Session is a superstruct to simplify passing around other structs
type Session struct {
	Telegram          *telegram.Bot                       // Telegram bot this session runs
	Discord           *discord.Bot                        // Discord bot this session runs
	Spam              *bots.Spam                          // Anti-spam struct for session
	Config            *Config                             // Configuration for session
	Cache             *db.Cache                           // Launch cache
	Db                *db.Database                        // Pointer to the database object
	Scheduler         *gocron.Scheduler                   // Gocron scheduler
	Tasks             []*gocron.Job                       // List of Gocron jobs
	NotificationTasks map[time.Time]*gocron.Job           // Map a time to a scheduled Gocron job
	Scheduled         []string                            // A list of launch IDs that have a notification scheduled
	Version           string                              // Version number
	Started           time.Time                           // Unix timestamp of startup time
	UseDevEndpoint    bool                                // Configure to use LL2's development endpoint
	Github            string                              // Github link
	Mutex             sync.Mutex                          // Avoid concurrent writes
}

// Config contains the configuration parameters used by the program
type Config struct {
	Token              ApiTokens  // API tokens
	DbFolder           string     // Folder path the DB lives in
	Owner              int64      // Telegram owner id
	BroadcastTokenPool int        // Broadcast rate-limit, msg/sec (<= 30)
	BroadcastBurstPool int        // Broadcast bursting limit, msg/sec
	Mutex              sync.Mutex // Mutex to avoid concurrent writes
	ConfigPath         string     `json:"-"` // Path to the config file (not saved in JSON)
}

// ApiTokens contains the API tokens used by the bot(s)
type ApiTokens struct {
	Telegram string
	Discord  string
}

func (session *Session) Initialize() {
	// Load config with default paths
	session.Config = LoadConfigFromPath("", "")

	// Init notification task map
	session.NotificationTasks = make(map[time.Time]*gocron.Job)

	// Initialize cache
	session.Cache = &db.Cache{
		Launches:  []*db.Launch{},
		LaunchMap: make(map[string]*db.Launch),
		Users:     &users.UserCache{},
	}

	// Open database (TODO remove owner tag)
	session.Db = &db.Database{Owner: session.Config.Owner, Cache: session.Cache}
	session.Db.Open(session.Config.DbFolder)
	session.Cache.Database = session.Db

	// Create and initialize the anti-spam system
	session.Spam = &bots.Spam{}
	session.Spam.Initialize(
		session.Config.BroadcastTokenPool, session.Config.BroadcastBurstPool)

	// Initialize the Telegram bot
	session.Telegram = &telegram.Bot{
		Owner: session.Config.Owner,
		Spam:  session.Spam,
		Cache: session.Cache,
		Db:    session.Db,
	}

	// Init stats
	session.Telegram.Stats = session.Db.LoadStatisticsFromDisk("tg")
	session.Telegram.Stats.RunningVersion = session.Version
	session.Telegram.Stats.StartedAt = session.Started

	// Initialize Telegram bot
	session.Telegram.Initialize(session.Config.Token.Telegram)
}

// SaveConfig dumps the config to disk
func SaveConfig(config *Config) {
	SaveConfigToPath(config, config.ConfigPath)
}

// SaveConfigToPath dumps the config to disk at the specified path
func SaveConfigToPath(config *Config, configPath string) {
	jsonbytes, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		log.Fatal().Err(err).Msg("Error marshaling json")
	}

	var configf string
	if configPath != "" {
		configf = configPath
	} else {
		wd, err := os.Getwd()
		if err != nil {
			log.Fatal().Err(err).Msg("Error getting working directory")
		}
		configf = filepath.Join(wd, "data", "config.json")
	}

	// Ensure directory exists
	dir := filepath.Dir(configf)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
	}

	file, err := os.Create(configf)
	if err != nil {
		log.Fatal().Err(err).Msg("Error creating config file")
	}

	// Write, close
	_, err = file.Write(jsonbytes)
	if err != nil {
		log.Fatal().Err(err).Msg("Error writing config to disk")
	}

	file.Close()
}

// InitializeWithPaths initializes the session with custom config and data paths
func (session *Session) InitializeWithPaths(configPath, dataPath string) {
	// Load config from specified paths
	session.Config = LoadConfigFromPath(configPath, dataPath)

	// Init notification task map
	session.NotificationTasks = make(map[time.Time]*gocron.Job)

	// Initialize cache
	session.Cache = &db.Cache{
		Launches:  []*db.Launch{},
		LaunchMap: make(map[string]*db.Launch),
		Users:     &users.UserCache{},
	}

	// Open database (TODO remove owner tag)
	session.Db = &db.Database{Owner: session.Config.Owner, Cache: session.Cache}
	session.Db.Open(session.Config.DbFolder)
	session.Cache.Database = session.Db

	// Create and initialize the anti-spam system
	session.Spam = &bots.Spam{}
	session.Spam.Initialize(
		session.Config.BroadcastTokenPool, session.Config.BroadcastBurstPool)

	// Initialize the Telegram bot
	session.Telegram = &telegram.Bot{
		Owner: session.Config.Owner,
		Spam:  session.Spam,
		Cache: session.Cache,
		Db:    session.Db,
	}

	// Init stats
	session.Telegram.Stats = session.Db.LoadStatisticsFromDisk("tg")
	session.Telegram.Stats.RunningVersion = session.Version
	session.Telegram.Stats.StartedAt = session.Started

	// Initialize Telegram bot
	session.Telegram.Initialize(session.Config.Token.Telegram)
}

// LoadConfig loads the config and returns a pointer to it
func LoadConfig() *Config {
	return LoadConfigFromPath("", "")
}

// LoadConfigFromPath loads the config from a specified path
func LoadConfigFromPath(configPath, dataPath string) *Config {
	// Get log file's path relative to working dir
	wd, _ := os.Getwd()

	// Check for existing data in default location for migration
	defaultDataPath := filepath.Join(wd, "data")
	defaultConfigPath := filepath.Join(defaultDataPath, "config.json")
	defaultDbPath := filepath.Join(defaultDataPath, "launchbot.db")
	
	// Use default paths if not specified
	if dataPath == "" {
		dataPath = defaultDataPath
	} else if !filepath.IsAbs(dataPath) {
		dataPath = filepath.Join(wd, dataPath)
	}

	// Ensure data directory exists
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		_ = os.MkdirAll(dataPath, os.ModePerm)
	}

	// Determine config file path
	var configf string
	if configPath != "" {
		if filepath.IsAbs(configPath) {
			configf = configPath
		} else {
			configf = filepath.Join(wd, configPath)
		}
	} else {
		configf = filepath.Join(dataPath, "config.json")
	}

	// Check if we need to migrate from default location
	if dataPath != defaultDataPath {
		// Check if default location has data but new location doesn't
		_, defaultConfigErr := os.Stat(defaultConfigPath)
		_, defaultDbErr := os.Stat(defaultDbPath)
		_, newConfigErr := os.Stat(configf)
		
		if defaultConfigErr == nil && newConfigErr != nil {
			// Config exists in default but not in new location
			log.Info().Msgf("Detected existing config at %s, migrating to %s", defaultConfigPath, configf)
			
			// Offer to migrate
			fmt.Printf("Found existing data in %s\n", defaultDataPath)
			fmt.Printf("Would you like to migrate it to %s? (y/n): ", dataPath)
			
			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))
			
			if response == "y" || response == "yes" {
				// Copy config file
				if err := copyFile(defaultConfigPath, configf); err != nil {
					log.Error().Err(err).Msg("Failed to migrate config file")
				} else {
					log.Info().Msg("Config file migrated successfully")
				}
				
				// Copy database if it exists
				if defaultDbErr == nil {
					newDbPath := filepath.Join(dataPath, "launchbot.db")
					if err := copyFile(defaultDbPath, newDbPath); err != nil {
						log.Error().Err(err).Msg("Failed to migrate database file")
					} else {
						log.Info().Msg("Database file migrated successfully")
					}
				}
			}
		}
	}

	if _, err := os.Stat(configf); os.IsNotExist(err) {
		// Config doesn't exist: create
		fmt.Print("Enter bot token: ")

		reader := bufio.NewReader(os.Stdin)
		inp, _ := reader.ReadString('\n')
		botToken := strings.TrimSuffix(inp, "\n")

		// Create, marshal
		config := Config{
			Token:              ApiTokens{Telegram: botToken},
			BroadcastTokenPool: 20,
			BroadcastBurstPool: 5,
			DbFolder:           dataPath,
			ConfigPath:         configf,
		}

		fmt.Println("Success! Starting bot...")

		go SaveConfigToPath(&config, configf)
		return &config
	}

	// Config exists: load
	fbytes, err := ioutil.ReadFile(configf)

	if err != nil {
		log.Fatal().Err(err).Msg("Error reading config file")
	}

	// New config struct
	var config Config

	// Unmarshal into our config struct
	err = json.Unmarshal(fbytes, &config)

	if err != nil {
		log.Fatal().Err(err).Msg("Error unmarshaling config json")
	}

	if config.BroadcastTokenPool == 0 {
		// Ensure migrating from older versions has _some_ value for token pool size
		config.BroadcastTokenPool = 20
		config.BroadcastBurstPool = 5
	}

	// Store the config path and ensure absolute DbFolder path
	config.ConfigPath = configf
	if config.DbFolder == "" || config.DbFolder == "data" {
		config.DbFolder = dataPath
	} else if !filepath.IsAbs(config.DbFolder) {
		config.DbFolder = filepath.Join(wd, config.DbFolder)
	}

	return &config
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Create destination directory if needed
	destDir := filepath.Dir(dst)
	if err := os.MkdirAll(destDir, os.ModePerm); err != nil {
		return err
	}

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}
