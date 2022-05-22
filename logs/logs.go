package logs

import (
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
)

func SetupLogFile(logFolder string) *os.File {
	// Logpath relative to current working directory
	wd, _ := os.Getwd()
	logPath := filepath.Join(wd, logFolder)

	// If folders do not exist, create them
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		_ = os.Mkdir(logPath, os.ModePerm)
	}

	logFilePath := filepath.Join(logPath, "launchbot-logs.log")
	logf, err := os.OpenFile(logFilePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)

	if err != nil {
		log.Error().Err(err)
	}

	return logf
}

func GetLogSize(filepath string) int64 {
	// Default filepath is logs/bot.log
	if filepath == "" {
		filepath = "logs/launchbot-logs.log"
	}

	fileInfo, err := os.Stat(filepath)

	if err != nil {
		log.Error().Err(err).Msg("Getting log-file stats failed")
		return 0
	}

	// Set the size
	return fileInfo.Size()
}
