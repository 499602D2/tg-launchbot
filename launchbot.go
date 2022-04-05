package launchbot

import (
	"flag"
	"fmt"
	"launchbot/config"
	"launchbot/logs"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Variables injected at build-time
var GitSHA string

func main() {
	// Create session
	session := config.Session{}
	session.Config = config.LoadConfig()
	session.Version = fmt.Sprintf("3.0.0-pre (%s)", GitSHA[0:7])

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

	log.Printf("🤖 GoLaunch %s started at %s", session.Version, time.Now())

	// Update session
	session.Started = time.Now().Unix()

	// Start notification scheduler in a new thread

	// Create and startbots
}
