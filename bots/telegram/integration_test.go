//go:build integration

package telegram

import (
	"encoding/json"
	"fmt"
	"launchbot/bots"
	"launchbot/db"
	"launchbot/sendables"
	"launchbot/stats"
	"launchbot/users"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// Test configuration structure
type TestConfig struct {
	Token struct {
		Telegram string `json:"Telegram"`
		Discord  string `json:"Discord"`
	} `json:"Token"`
	DbFolder           string `json:"DbFolder"`
	Owner              int64  `json:"Owner"`
	BroadcastTokenPool int    `json:"BroadcastTokenPool"`
	BroadcastBurstPool int    `json:"BroadcastBurstPool"`
}

// loadTestConfig loads the config file for integration testing
func loadTestConfig(t *testing.T) (*TestConfig, error) {
	configPath := "../../data/config.json"

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg TestConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.Token.Telegram == "" {
		return nil, fmt.Errorf("telegram token not found in config")
	}

	if cfg.Owner == 0 {
		return nil, fmt.Errorf("owner not found in config")
	}

	return &cfg, nil
}

// setupTestBot creates a bot instance for testing
func setupTestBot(t *testing.T, cfg *TestConfig) (*Bot, error) {
	// Create Telebot instance
	pref := tb.Settings{
		Token:  cfg.Token.Telegram,
		Poller: &tb.LongPoller{Timeout: 10 * time.Second},
	}

	bot, err := tb.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	// Create test database
	testDb := &db.Database{
		Path: ":memory:",
	}

	// Initialize cache
	cache := &db.Cache{
		LaunchMap: make(map[string]*db.Launch),
		Users: &users.UserCache{
			Users:   []*users.User{},
			InCache: []string{},
		},
		Database: testDb,
	}

	// Initialize spam manager
	spam := &bots.Spam{}
	spam.Initialize(20, 5)

	// Create bot wrapper
	tg := &Bot{
		Bot:      bot,
		Username: bot.Me.Username,
		Stats:    &stats.Statistics{},
		Spam:     spam,
		Db:       testDb,
		Cache:    cache,
		Quit: Quit{
			Channel:   make(chan int),
			WaitGroup: &sync.WaitGroup{},
		},
	}

	return tg, nil
}

// TestTelegramIntegration tests various Telegram bot functionalities
func TestTelegramIntegration(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Load config
	cfg, err := loadTestConfig(t)
	if err != nil {
		t.Skipf("Skipping integration test: %v", err)
	}

	// Setup bot
	bot, err := setupTestBot(t, cfg)
	if err != nil {
		t.Fatalf("Failed to setup bot: %v", err)
	}

	// Start the threaded sender in the background
	go bot.ThreadedSender()
	defer func() {
		// Gracefully shutdown
		bot.Quit.Channel <- 0
		time.Sleep(2 * time.Second)
	}()

	// Create owner user
	ownerUser := &users.User{
		Id:   strconv.FormatInt(cfg.Owner, 10),
		Type: users.Private,
	}

	t.Run("SendBasicMessage", func(t *testing.T) {
		// Test sending a basic message
		text := fmt.Sprintf("🧪 Integration test message - %s", time.Now().Format(time.RFC3339))
		sent, err := bot.Bot.Send(tb.ChatID(cfg.Owner), text)
		if err != nil {
			t.Fatalf("Failed to send test message: %v", err)
		}

		log.Info().Msgf("Successfully sent test message (ID: %d)", sent.ID)

		// Clean up
		err = bot.Bot.Delete(sent)
		if err != nil {
			t.Logf("Failed to delete test message: %v", err)
		}
	})

	t.Run("NotificationWithBatchDeletion", func(t *testing.T) {
		// Step 1: Send test notification message to owner
		text := fmt.Sprintf("🚀 Test notification for batch deletion - %s", time.Now().Format(time.RFC3339))
		sent, err := bot.Bot.Send(tb.ChatID(cfg.Owner), text)
		if err != nil {
			t.Fatalf("Failed to send test message: %v", err)
		}

		log.Info().Msgf("Sent test notification message (ID: %d)", sent.ID)

		// Step 2: Create batch deletion sendable (mimicking NotificationPostProcessing)
		messageIDs := map[string]string{
			ownerUser.Id: fmt.Sprintf("%d", sent.ID),
		}

		deletionSendable := &sendables.Sendable{
			Type:       sendables.Delete,
			IsBatch:    true,
			MessageIDs: messageIDs,
			Recipients: []*users.User{ownerUser},
			Platform:   "tg",
			LaunchId:   "test-launch",
			Tokens:     1,
		}

		// Step 3: Process the batch deletion
		log.Info().Msg("Starting batch deletion of old notification")
		startTime := time.Now()

		// Process the deletion directly (batch deletion doesn't use workers in our implementation)
		results := make(chan string, 1)
		bot.processBatchDeletion(deletionSendable, results, startTime)

		duration := time.Since(startTime)
		log.Info().Msgf("Batch deletion completed in %v", duration)

		// Verify the deletion was processed quickly (should be fast for 1 message)
		if duration > 5*time.Second {
			t.Errorf("Batch deletion took too long: %v (expected < 5s for 1 message)", duration)
		}
	})

	t.Run("CommandMessage", func(t *testing.T) {
		// Test sending a command reply message
		cmdMessage := &sendables.Message{
			TextContent: "📋 This is a test command response",
			SendOptions: tb.SendOptions{
				ParseMode: tb.ModeMarkdown,
			},
		}

		cmdSendable := &sendables.Sendable{
			Type:       sendables.Command,
			Message:    cmdMessage,
			Recipients: []*users.User{ownerUser},
			Platform:   "tg",
		}

		// Enqueue command message
		bot.Enqueue(cmdSendable, true)

		// Give it time to process
		time.Sleep(500 * time.Millisecond)

		log.Info().Msg("Command message test completed")
	})
}

// TestErrorHandling tests error scenarios
func TestErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg, err := loadTestConfig(t)
	if err != nil {
		t.Skipf("Skipping integration test: %v", err)
	}

	bot, err := setupTestBot(t, cfg)
	if err != nil {
		t.Fatalf("Failed to setup bot: %v", err)
	}

	t.Run("DeleteNonExistentMessage", func(t *testing.T) {
		// Test deleting a message that doesn't exist
		nonExistent := &tb.Message{
			ID:   999999999,
			Chat: &tb.Chat{ID: cfg.Owner},
		}

		err := bot.Bot.Delete(nonExistent)
		if err == nil {
			t.Error("Expected error when deleting non-existent message")
		}
		log.Info().Msgf("Got expected error: %v", err)
	})

	t.Run("EmptyBatchDeletion", func(t *testing.T) {
		// Test with no messages to delete
		emptySendable := &sendables.Sendable{
			Type:       sendables.Delete,
			IsBatch:    true,
			MessageIDs: make(map[string]string),
			Recipients: []*users.User{},
			Platform:   "tg",
		}

		results := make(chan string, 1)
		startTime := time.Now()

		// This should handle gracefully
		bot.processBatchDeletion(emptySendable, results, startTime)

		// Should complete quickly with no errors
		select {
		case _, ok := <-results:
			if ok {
				t.Error("Expected results channel to be closed for empty deletion")
			}
		case <-time.After(1 * time.Second):
			t.Error("Empty batch deletion took too long")
		}
	})
}
