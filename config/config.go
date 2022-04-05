package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"launchbot/bots"
	"launchbot/db"
	"launchbot/launch"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Session struct {
	/* A superstruct to simplify passing around other structs */
	Telegram *bots.TelegramBot // Telegram bot this session runs
	Discord  *bots.DiscordBot  // Discord bot this session runs
	Spam     *bots.AntiSpam    // Anti-spam struct for session
	Config   *Config           // Configuration for session

	// Caching and database
	LaunchCache *launch.LaunchCache
	Db          *db.Database

	// Boring configuration stuff
	Version string     // Version number
	Started int64      // Unix timestamp of startup time
	Debug   bool       // Debugging?
	Mutex   sync.Mutex // Avoid concurrent writes
}

type Config struct {
	Token Tokens     // API tokens
	Owner int64      // Owner of the bot: skips logging
	Mutex sync.Mutex // Mutex to avoid concurrent writes
}

type Tokens struct {
	Telegram string
	Discord  string
}

func DumpConfig(config *Config) {
	// Dumps config to disk
	jsonbytes, err := json.MarshalIndent(config, "", "\t")

	if err != nil {
		log.Printf("⚠️ Error marshaling json! Err: %s\n", err)
	}

	wd, _ := os.Getwd()
	configf := filepath.Join(wd, "config", "bot-config.json")

	file, err := os.Create(configf)

	if err != nil {
		log.Println(err)
		os.Exit(1)
	}

	// Write, close
	_, err = file.Write(jsonbytes)
	if err != nil {
		log.Printf("⚠️ Error writing config to disk! Err: %s\n", err)
	}

	file.Close()
}

func LoadConfig() *Config {
	/* Loads the config, returns a pointer to it */

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
			Token: Tokens{Telegram: botToken},
			Owner: 0,
		}

		fmt.Println("Success! Starting bot...")

		go DumpConfig(&config)
		return &config
	}

	// Config exists: load
	fbytes, err := ioutil.ReadFile(configf)
	if err != nil {
		log.Println("⚠️ Error reading config file:", err)
		os.Exit(1)
	}

	// New config struct
	var config Config

	// Unmarshal into our config struct
	err = json.Unmarshal(fbytes, &config)
	if err != nil {
		log.Println("⚠️ Error unmarshaling config json: ", err)
		os.Exit(1)
	}

	return &config
}
