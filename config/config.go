package config

import (
	"bufio"
	"encoding/json"
	"fmt"
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
}

// ApiTokens contains the API tokens used by the bot(s)
type ApiTokens struct {
	Telegram string
	Discord  string
}

func (session *Session) Initialize() {
	// Load config
	session.Config = LoadConfig()

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
	jsonbytes, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		log.Fatal().Err(err).Msg("Error marshaling json")
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Fatal().Err(err).Msg("Error getting working directory")
	}

	configf := filepath.Join(wd, "data", "config.json")

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

// LoadConfig loads the config and returns a pointer to it
func LoadConfig() *Config {
	// Get log file's path relative to working dir
	wd, _ := os.Getwd()

	configPath := filepath.Join(wd, "data")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		_ = os.Mkdir(configPath, os.ModePerm)
	}

	configf := filepath.Join(configPath, "config.json")

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
			DbFolder:           "data",
		}

		fmt.Println("Success! Starting bot...")

		go SaveConfig(&config)
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

	return &config
}
