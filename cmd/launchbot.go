package main

import (
	"flag"
	"fmt"
	"launchbot/api"
	"launchbot/config"
	"launchbot/logging"
	"launchbot/sendables"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/rs/zerolog/pkgerrors"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Injected at build-time
var GitSHA = "0000000000"
var Version = "dev"

// Listens for incoming interrupt signals
func signalListener(session *config.Session) {
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
		log.Info().Msg("Saving stats to disk...")
		session.Db.SaveStatsToDisk(session.Telegram.Stats)

		// Save all cached users
		log.Info().Msg("Starting user-cache flush...")
		session.Cache.CleanUserCache(session.Db, true, true)

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
		configPath     string
		dataPath       string
	)

	// Command line arguments
	flag.BoolVar(&debug, "debug", false, "Specify to enable debug mode")
	flag.BoolVar(&noUpdates, "no-api-updates", false, "Specify to disable API updates")
	flag.BoolVar(&updateNow, "update-now", false, "Specify to run an API update now")
	flag.BoolVar(&useDevEndpoint, "dev-endpoint", false, "Specify to use LL2's dev endpoint")
	flag.BoolVar(&verboseSpamLog, "verbose-spam-log", false, "Specify to enable verbose spam and permission logging ")
	flag.StringVar(&configPath, "config", "", "Path to config file (defaults to $LAUNCHBOT_CONFIG or ./data/config.json)")
	flag.StringVar(&dataPath, "data", "", "Path to data directory (defaults to $LAUNCHBOT_DATA_DIR or ./data)")

	flag.Parse()

	// Check environment variables if flags not set
	if configPath == "" {
		configPath = os.Getenv("LAUNCHBOT_CONFIG")
	}
	if dataPath == "" {
		dataPath = os.Getenv("LAUNCHBOT_DATA_DIR")
	}

	// Enable stack traces
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	// Set-up logging
	if !debug {
		// If not debugging, log to file
		// Use custom data path if specified, otherwise default to "data"
		logPath := dataPath
		if logPath == "" {
			logPath = "data"
		}
		logf := logging.SetupLogFile(logPath)
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: logf, NoColor: true, TimeFormat: time.RFC822Z})

		defer logf.Close()
	} else {
		// If debugging, output to console
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC822Z})
	}

	log.Info().Msgf("🤖 LaunchBot %s started", Version)

	// Create session
	session := &config.Session{
		Started:        time.Now(),
		Version:        fmt.Sprintf("%s (%s)", Version, GitSHA[0:7]),
		Github:         "github.com/499602D2/tg-launchbot",
		UseDevEndpoint: useDevEndpoint,
	}

	// Signal handler (ctrl+c, etc.)
	signalListener(session)

	// Initialize session with custom paths
	session.InitializeWithPaths(configPath, dataPath)

	// Assign remaining CLI flags
	session.Spam.VerboseLog = verboseSpamLog

	if !noUpdates {
		// Create a new task scheduler, assign to session
		session.Scheduler = gocron.NewScheduler(time.UTC)

		// Start the scheduler async
		session.Scheduler.StartAsync()

		// Populate the cache
		session.Cache.Populate()

		if updateNow {
			// Run API update manually and enable auto-scheduler
			log.Info().Msg("--Update-now specified, running API update")
			go api.Updater(session, true)
		} else {
			// Start scheduler normally, but use the startup flag
			go api.Scheduler(session, true, nil, false)
		}
	} else {
		log.Warn().Msg("API updates disabled")
	}

	// Dump statistics to disk once every 10 minutes
	scheduler := gocron.NewScheduler(time.UTC)
	_, err := scheduler.Every(10).Minutes().Do(session.Db.SaveStatsToDisk, session.Telegram.Stats)

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

	if session.Telegram.Owner != 0 {
		// If owner is configured, notify of startup
		startSendable := sendables.TextOnlySendable(
			"🤖 LaunchBot started",
			session.Db.LoadUser(fmt.Sprint(session.Telegram.Owner), "tg"),
		)

		session.Telegram.Enqueue(startSendable, true)
	}

	for {
		time.Sleep(time.Second * 60)
	}
}
