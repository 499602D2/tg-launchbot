package main

import (
	"flag"
	"fmt"
	"launchbot/api"
	"launchbot/bots"
	"launchbot/config"
	"launchbot/db"
	"launchbot/logs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/procyon-projects/chrono"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Variables injected at build-time
var GitSHA = "0000000000"

// Listens for incoming interrupt signals
func setupSignalHandler(session *config.Session) {
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-channel
		// Log shutdown
		log.Info().Msg("🚦 Received interrupt signal: stopping updaters...")

		/* TODO uncomment in production
		if session.Telegram != nil {
			session.Telegram.Bot.Stop()
		}

		if session.Discord != nil {
			session.Discord.Bot.Close()
		} */

		// Exit
		os.Exit(0)
	}()
}

func initSession(version string) *config.Session {
	// Create session
	session := config.Session{
		Started: time.Now().Unix(),
		Version: fmt.Sprintf("%s (%s)", version, GitSHA[0:7]),
	}

	// Signal handler (ctrl+c, etc.)
	setupSignalHandler(&session)

	// Load config
	session.Config = config.LoadConfig()

	// Open database (TODO remove owner tag)
	session.Db = &db.Database{Owner: session.Config.Owner}
	session.Db.Open(session.Config.DbFolder)

	// Initialize cache
	session.LaunchCache = &db.Cache{
		Launches:  []*db.Launch{},
		LaunchMap: make(map[string]*db.Launch),
	}

	// Create and initialize the anti-spam system
	session.Spam = &bots.AntiSpam{}
	session.Spam.Initialize()

	// Initialize the Telegram bot
	session.Telegram = &bots.TelegramBot{}
	session.Telegram.Spam = session.Spam
	session.Telegram.Cache = session.LaunchCache
	session.Telegram.Initialize(session.Config.Token.Telegram)

	return &session
}

func main() {
	const version = "3.0.0-pre"

	// CLI flags
	var debug bool
	var noUpdates bool

	// Command line arguments
	flag.BoolVar(&debug, "debug", false, "Specify to enable debug mode")
	flag.BoolVar(&noUpdates, "no-api-updates", false, "Specify to disable API updates")
	flag.Parse()

	// Set-up logging
	if !debug {
		// If not debugging, log to file
		logf := logs.SetupLogFile("logs")
		defer logf.Close()

		log.Logger = log.Output(zerolog.ConsoleWriter{Out: logf, NoColor: true, TimeFormat: time.RFC822Z})
	} else {
		// If debugging, output to console
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})
	}

	/*
		asciiArt := `
		888                                          888      888888b.            888          .d8888b.
		888                                          888      888  "88b           888         d88P  Y88b
		888                                          888      888  .88P           888         888    888
		888       8888b.  888  888 88888b.   .d8888b 88888b.  8888888K.   .d88b.  888888      888         .d88b.
		888          "88b 888  888 888 "88b d88P"    888 "88b 888  "Y88b d88""88b 888         888  88888 d88""88b
		888      .d888888 888  888 888  888 888      888  888 888    888 888  888 888  888888 888    888 888  888
		888      888  888 Y88b 888 888  888 Y88b.    888  888 888   d88P Y88..88P Y88b.       Y88b  d88P Y88..88P
		88888888 "Y888888  "Y88888 888  888  "Y8888P 888  888 8888888P"   "Y88P"   "Y888       "Y8888P88  "Y88P"`

	log.Info().Msg(strings.Replace(asciiArt, "	", "", -1)) */
	log.Info().Msgf("🤖 LaunchBot-Go %s started", version)

	// Create session
	session := initSession(version)

	// Start notification scheduler in a new thread
	if !noUpdates {
		// Create a new task scheduler, assign to session
		session.Scheduler = chrono.NewDefaultTaskScheduler()

		// Before doing finer scheduling, check if we need to update immediately
		if session.Db.RequireImmediateUpdate() {
			log.Info().Msg("Db requires an immediate update: updating now...")

			// Run API update manually and enable auto-scheduler
			// Running this in a go-routine might cause the cache to not be initialized,
			// so we're doing this synchronously.
			api.Updater(session, true)
		} else {
			// No need to update: schedule next call
			log.Info().Msg("Database does not require an immediate update")

			// Since db won't be immediately updated, we will still need to load the cache
			// TODO implement
			// session.LaunchCache.Populate()

			// Schedule next call normally: cache is now populated
			go api.Scheduler(session)
		}
	} else {
		log.Warn().Msg("API updates disabled")
	}

	// Start the sender in a go-routine
	go bots.TelegramSender(session.Telegram)

	// Start the bot in a go-routine
	go session.Telegram.Bot.Start()
	log.Debug().Msg("Telegram bot started")

	for {
		time.Sleep(time.Second * 60)
	}
}
