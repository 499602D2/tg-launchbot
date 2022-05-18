package main

import (
	"flag"
	"fmt"
	"launchbot/api"
	"launchbot/bots"
	"launchbot/bots/telegram"
	"launchbot/config"
	"launchbot/db"
	"launchbot/logs"
	"launchbot/users"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/rs/zerolog/pkgerrors"

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

		// Save stats to disk
		session.Db.SaveStatsToDisk(session.Telegram.Stats)

		// Save all cached users
		session.LaunchCache.CleanUserCache(session.Db, true)

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
		Started:           time.Now(),
		Version:           fmt.Sprintf("%s (%s)", version, GitSHA[0:7]),
		NotificationTasks: make(map[time.Time]*chrono.ScheduledTask),
	}

	// Signal handler (ctrl+c, etc.)
	setupSignalHandler(&session)

	// Load config
	session.Config = config.LoadConfig()

	// Initialize cache
	session.LaunchCache = &db.Cache{
		Launches:  []*db.Launch{},
		LaunchMap: make(map[string]*db.Launch)}

	session.LaunchCache.Users = &users.UserCache{}

	// Open database (TODO remove owner tag)
	session.Db = &db.Database{Owner: session.Config.Owner, Cache: session.LaunchCache}
	session.Db.Open(session.Config.DbFolder)
	session.LaunchCache.Database = session.Db

	// Create and initialize the anti-spam system
	session.Spam = &bots.Spam{}
	session.Spam.Initialize()

	// Initialize the Telegram bot
	session.Telegram = &telegram.Bot{}
	session.Telegram.Owner = session.Config.Owner
	session.Telegram.Spam = session.Spam
	session.Telegram.Cache = session.LaunchCache
	session.Telegram.Db = session.Db

	// Init stats
	session.Telegram.Stats = session.Db.LoadStatisticsFromDisk("tg")
	session.Telegram.Stats.RunningVersion = session.Version
	session.Telegram.Stats.StartedAt = session.Started

	session.Telegram.Initialize(session.Config.Token.Telegram)

	return &session
}

func main() {
	const version = "3.0.0"

	// CLI flags
	var (
		debug          bool
		noUpdates      bool
		updateNow      bool
		useDevEndpoint bool
	)

	// Command line arguments
	flag.BoolVar(&debug, "debug", false, "Specify to enable debug mode")
	flag.BoolVar(&noUpdates, "no-api-updates", false, "Specify to disable API updates")
	flag.BoolVar(&updateNow, "update-now", false, "Specify to run an API update now")
	flag.BoolVar(&useDevEndpoint, "dev-endpoint", false, "Specify to use LL2's dev endpoint")
	flag.Parse()

	// Enable stack traces
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	// Set-up logging
	if !debug {
		// If not debugging, log to file
		logf := logs.SetupLogFile("logs")
		defer logf.Close()

		log.Logger = log.Output(zerolog.ConsoleWriter{Out: logf, NoColor: true, TimeFormat: time.RFC822Z})
	} else {
		// If debugging, output to console
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})
		asciiArt := `
			888                                          888      888888b.            888          .d8888b.
			888                                          888      888  "88b           888         d88P  Y88b
			888                                          888      888  .88P           888         888    888
			888       8888b.  888  888 88888b.   .d8888b 88888b.  8888888K.   .d88b.  888888      888         .d88b.
			888          "88b 888  888 888 "88b d88P"    888 "88b 888  "Y88b d88""88b 888         888  88888 d88""88b
			888      .d888888 888  888 888  888 888      888  888 888    888 888  888 888  888888 888    888 888  888
			888      888  888 Y88b 888 888  888 Y88b.    888  888 888   d88P Y88..88P Y88b.       Y88b  d88P Y88..88P
			88888888 "Y888888  "Y88888 888  888  "Y8888P 888  888 8888888P"   "Y88P"   "Y888       "Y8888P88  "Y88P"`

		log.Info().Msg(strings.Replace(asciiArt, "	", "", -1))
	}

	log.Info().Msgf("🤖 LaunchBot-Go %s started", version)

	// Create session
	session := initSession(version)
	session.UseDevEndpoint = useDevEndpoint

	if !noUpdates {
		// Create a new task scheduler, assign to session
		session.Scheduler = chrono.NewDefaultTaskScheduler()

		// Populate the cache
		session.LaunchCache.Populate()

		if updateNow {
			// Run API update manually and enable auto-scheduler
			log.Info().Msg("--Update-now specified, running API update")
			go api.Updater(session, true)
		} else {
			// Start scheduler normally, but use the startup flag
			go api.Scheduler(session, true)
		}
	} else {
		log.Warn().Msg("API updates disabled")
	}

	// Dump statistics to disk once every 30 minutes
	scheduler := gocron.NewScheduler(time.UTC)
	_, err := scheduler.Every(30).Minutes().Do(session.Db.SaveStatsToDisk, session.Telegram.Stats)

	if err != nil {
		log.Fatal().Err(err).Msg("Starting statistics gocron job failed")
	}

	// Clean user-cache once an hour
	_, err = scheduler.Every(1).Hour().Do(session.LaunchCache.CleanUserCache, session.Db, false)

	if err != nil {
		log.Fatal().Err(err).Msg("Starting cache-flush gocron job failed")
	}

	// Run scheduled jobs async
	scheduler.StartAsync()

	// Start the Telegram-sender in a go-routine
	go telegram.Sender(session.Telegram)

	// Start the bot in a go-routine
	go session.Telegram.Bot.Start()
	log.Debug().Msg("Telegram bot started")

	for {
		time.Sleep(time.Second * 60)
	}
}
