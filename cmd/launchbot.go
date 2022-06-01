package main

import (
	"flag"
	"fmt"
	"launchbot/api"
	"launchbot/config"
	"launchbot/logs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/rs/zerolog/pkgerrors"

	"github.com/procyon-projects/chrono"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Injected at build-time
var GitSHA = "0000000000"

const version = "3.1.0"

// Listens for incoming interrupt signals
func setupSignalHandler(session *config.Session) {
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-channel
		// Log shutdown
		log.Info().Msg("🚦 Received interrupt signal, stopping the program...")

		// Close message sender
		session.Telegram.Quit.Channel <- 1

		// Sleep so that the closer can acquire a lock
		time.Sleep(time.Millisecond * time.Duration(100))

		// Once we can re-acquire a lock, the sender is closed
		// TODO add a "press ctrl+c again to force-quit"
		session.Telegram.Quit.Mutex.Lock()

		// Wait for signal
		success := <-session.Telegram.Quit.Channel

		if success == -1 {
			log.Info().Msgf("Message sender shut down gracefully")
			close(session.Telegram.Quit.Channel)
		}

		// Save stats to disk
		session.Db.SaveStatsToDisk(session.Telegram.Stats)

		// Save all cached users
		session.Cache.CleanUserCache(session.Db, true)

		// if session.Telegram != nil {
		// 	log.Debug().Msg("Stopping Telegram bot...")
		// 	session.Telegram.Bot.Stop()
		// }

		// if session.Discord != nil {
		// 	session.Discord.Bot.Close()
		// }

		// Exit
		os.Exit(0)
	}()
}

func main() {
	// CLI flags
	var (
		debug          bool
		noUpdates      bool
		updateNow      bool
		useDevEndpoint bool
		verboseSpamLog bool
	)

	// Command line arguments
	flag.BoolVar(&debug, "debug", false, "Specify to enable debug mode")
	flag.BoolVar(&noUpdates, "no-api-updates", false, "Specify to disable API updates")
	flag.BoolVar(&updateNow, "update-now", false, "Specify to run an API update now")
	flag.BoolVar(&useDevEndpoint, "dev-endpoint", false, "Specify to use LL2's dev endpoint")
	flag.BoolVar(&verboseSpamLog, "verbose-spam-log", false, "Specify to enable verbose spam and permission logging ")

	flag.Parse()

	// Enable stack traces
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	// Set-up logging
	if !debug {
		// If not debugging, log to file
		logf := logs.SetupLogFile("data")
		defer logf.Close()

		log.Logger = log.Output(zerolog.ConsoleWriter{Out: logf, NoColor: true, TimeFormat: time.RFC822Z})
	} else {
		// If debugging, output to console
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})
	}

	log.Info().Msgf("🤖 LaunchBot %s started", version)

	// Create session
	session := &config.Session{
		Started:        time.Now(),
		Version:        fmt.Sprintf("%s (%s)", version, GitSHA[0:7]),
		Github:         "github.com/499602D2/tg-launchbot",
		UseDevEndpoint: useDevEndpoint,
	}

	// Signal handler (ctrl+c, etc.)
	setupSignalHandler(session)

	// Initialize session
	session.Initialize()

	// Assign remaining CLI flags
	session.Spam.VerboseLog = verboseSpamLog

	if !noUpdates {
		// Create a new task scheduler, assign to session
		session.Scheduler = chrono.NewDefaultTaskScheduler()

		// Populate the cache
		session.Cache.Populate()

		if updateNow {
			// Run API update manually and enable auto-scheduler
			log.Info().Msg("--Update-now specified, running API update")
			go api.Updater(session, true)
		} else {
			// Start scheduler normally, but use the startup flag
			go api.Scheduler(session, true, nil)
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

	// Run scheduled jobs async
	scheduler.StartAsync()

	// Start the Telegram-sender in a go-routine
	go session.Telegram.ThreadedSender()

	// Start the bot in a go-routine
	go session.Telegram.Bot.Start()
	log.Info().Msgf("Telegram bot started (@%s)", session.Telegram.Username)

	for {
		time.Sleep(time.Second * 60)
	}
}
