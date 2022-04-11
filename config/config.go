package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"launchbot/bots"
	"launchbot/db"
	"launchbot/ll2"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/procyon-projects/chrono"
	"github.com/rs/zerolog/log"
)

/* Session is a superstruct to simplify passing around other structs */
type Session struct {
	Telegram *bots.TelegramBot // Telegram bot this session runs
	Discord  *bots.DiscordBot  // Discord bot this session runs
	Spam     *bots.AntiSpam    // Anti-spam struct for session
	Config   *Config           // Configuration for session

	LaunchCache *ll2.LaunchCache // Launch cache
	Db          *db.Database     // Pointer to the database object

	Scheduler chrono.TaskScheduler   // Chrono scheduler
	Tasks     []chrono.ScheduledTask // List of tasks pending

	Version string     // Version number
	Started int64      // Unix timestamp of startup time
	Debug   bool       // Debugging?
	Mutex   sync.Mutex // Avoid concurrent writes
}

type Config struct {
	Token    Tokens     // API tokens
	DbFolder string     // Folder path the DB lives in
	Mutex    sync.Mutex // Mutex to avoid concurrent writes
}

type Tokens struct {
	Telegram string
	Discord  string
}

/* Dumps the config to disk */
func DumpConfig(config *Config) {
	jsonbytes, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		log.Fatal().Err(err).Msg("Error marshaling json")
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Fatal().Err(err).Msg("Error getting working directory")
	}

	configf := filepath.Join(wd, "config", "bot-config.json")

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

/* Loads the config, returns a pointer to it */
func LoadConfig() *Config {
	// Get log file's path relative to working dir
	wd, _ := os.Getwd()
	configPath := filepath.Join(wd, "config")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		_ = os.Mkdir(configPath, os.ModePerm)
	}

	configf := filepath.Join(configPath, "bot-config.json")
	if _, err := os.Stat(configf); os.IsNotExist(err) {
		// Config doesn't exist: create
		fmt.Print("Enter bot token: ")

		reader := bufio.NewReader(os.Stdin)
		inp, _ := reader.ReadString('\n')
		botToken := strings.TrimSuffix(inp, "\n")

		// Create, marshal
		config := Config{
			Token:    Tokens{Telegram: botToken},
			DbFolder: "data/launchbot.db",
		}

		fmt.Println("Success! Starting bot...")

		go DumpConfig(&config)
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

	return &config
}
