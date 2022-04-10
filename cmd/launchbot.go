package main

import (
	"flag"
	"fmt"
	"launchbot/api"
	"launchbot/bots"
	"launchbot/config"
	"launchbot/db"
	"launchbot/ll2"
	"launchbot/logs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Variables injected at build-time
var GitSHA = "0000000000"

func setupSignalHandler(session *config.Session) {
	// Listens for incoming interrupt signals
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-channel
		// Log shutdown
		log.Info().Msg("🚦 Received interrupt signal")

		// Close bot
		session.Telegram.Bot.Close()

		// Exit
		os.Exit(0)
	}()
}

func main() {
	// Create session
	session := config.Session{}
	session.Config = config.LoadConfig()
	session.Version = fmt.Sprintf("3.0.0-pre (%s)", GitSHA[0:7])

	// Signal handler (ctrl+c, etc.)
	setupSignalHandler(&session)

	// Command line arguments
	flag.BoolVar(&session.Debug, "debug", false, "Specify to enable debug mode")
	flag.Parse()

	// Set-up logging
	if !session.Debug {
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
	log.Info().Msgf("🤖 LaunchBot-Go %s started", session.Version)

	// Update session
	session.Started = time.Now().Unix()

	// Open database
	session.Db = &db.Database{}
	session.Db.Open(session.Config.DbFolder)

	// Initialize cache
	session.LaunchCache = &ll2.LaunchCache{Launches: make(map[string]*ll2.Launch)}

	// Start notification scheduler in a new thread
	api.Updater(&session) // TODO: don't run updater directly (gocron setup)

	// Create and initialize the anti-spam system
	session.Spam = &bots.AntiSpam{}
	session.Spam.Initialize()

	// Init the bot object with the queues
	session.Telegram = &bots.TelegramBot{}
	session.Telegram.Initialize()
	session.Telegram.Bot = bots.SetupTelegramBot(
		session.Config.Token.Telegram, session.Spam,
		session.Telegram.MessageQueue, session.Telegram,
	)

	// Start the sender in a go-routine
	go bots.TelegramSender(session.Telegram)

	log.Info().Msg("Starting Telegram bot...")
	session.Telegram.Bot.Start()
}
