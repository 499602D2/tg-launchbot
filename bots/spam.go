package bots

import (
	"context"
	"launchbot/stats"
	"launchbot/users"
	"sync"
	"time"

	"github.com/hako/durafmt"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// In-memory struct keeping track of banned chats and per-chat activity
type AntiSpam struct {
	ChatBanned               map[*users.User]bool    // Simple "if ChatBanned[chat] { do }" checks
	ChatBannedUntilTimestamp map[*users.User]int64   // How long banned chats are banned for
	ChatLogs                 map[*users.User]ChatLog // Map chat ID to a ChatLog struct
	Rules                    map[string]int64        // Arbitrary rules for code flexibility
	Limiter                  *rate.Limiter           // Main rate-limiter
	Mutex                    sync.Mutex              // Mutex to avoid concurrent map writes
}

// Per-chat struct keeping track of activity for spam management
type ChatLog struct {
	Limiter *rate.Limiter // Per-chat ratelimiter
}

// A call handled by preHandler
type Interaction struct {
	IsAdminOnly   bool
	IsCommand     bool
	IsGroup       bool   // Called from a group?
	CallerIsAdmin bool   // Is the caller an admin?
	Name          string // Name of command or callback
	Tokens        int    // Token-count this call requires
}

// Initialize the spam struct
func (spam *AntiSpam) Initialize() {
	// Create all maps for the spam struct
	spam.ChatBannedUntilTimestamp = make(map[*users.User]int64)
	spam.ChatLogs = make(map[*users.User]ChatLog)
	spam.ChatBanned = make(map[*users.User]bool)
	spam.Rules = make(map[string]int64)

	// Enforce a global rate-limiter: sustain 25 msg/sec, with 30 msg/sec bursts
	// A change of 5 msg/sec is 300 more messages in a minute (!)
	spam.Limiter = rate.NewLimiter(25, 30)
}

// Enforce a per-chat rate-limiter. Typically, roughly 20 messages per minute
// can be sent to one chat.
func (spam *AntiSpam) UserLimiter(user *users.User, tokens int) {
	// If limiter doesn't exist, create it
	if spam.ChatLogs[user].Limiter == nil {
		spam.Mutex.Lock()

		// Load user's chatLog, assign new limiter, save back
		chatLog := spam.ChatLogs[user]
		chatLog.Limiter = rate.NewLimiter(rate.Every(time.Second*3), 2)
		spam.ChatLogs[user] = chatLog

		spam.Mutex.Unlock()
	}

	// Wait until we can take as many tokens as we need
	start := time.Now()
	err := spam.ChatLogs[user].Limiter.WaitN(context.Background(), tokens)

	// TODO track average time user-limiter runs for? Track secs + run count -> mean + avg
	log.Debug().Msgf("-> Limiter executed after %s", durafmt.Parse(time.Since(start)).LimitFirstN(1))

	if err != nil {
		log.Error().Err(err).Msgf("Error using chat's Limiter.Wait()")
	}
}

// Enforces a global rate-limiter for the bot. Telegram has some hard rate-limits,
// including a 30 messages-per-second send limit for messages under 512 bytes.
func (spam *AntiSpam) GlobalLimiter(tokens int) {
	// Take the required amount of tokens, sleep if required
	err := spam.Limiter.WaitN(context.Background(), tokens)

	if err != nil {
		log.Error().Err(err).Msgf("Error using global Limiter.Wait()")
	}
}

// A wrapper method to run both the global and user limiter at once
func (spam *AntiSpam) RunBothLimiters(user *users.User, tokens int) {
	// The user-limiter is ran first, as it's far more restricting
	spam.UserLimiter(user, tokens)
	spam.GlobalLimiter(tokens)
}

// When an interaction is received, ensure the sender is qualified for it
func (spam *AntiSpam) PreHandler(interaction *Interaction, chat *users.User, stats *stats.Statistics) bool {
	if interaction.IsGroup {
		// In groups, we need to ensure that regular users cannot do funny things
		if interaction.IsAdminOnly || !chat.AnyoneCanSendCommands {
			// Admin-only interaction, or group doesn't allow users to interact with the bot
			if !interaction.CallerIsAdmin {
				if interaction.IsCommand {
					// Run the global limiter for message deletions, as they're trivial to spam.
					// Callbacks show an alert, which slows users down enough.
					spam.GlobalLimiter(1)
				}

				return false
			}
		}
	}

	// Ensure command counter cannot be iterated by spamming the stats refresh button
	if interaction.Name != "stats" || interaction.IsCommand {
		// Processing allowed: save statistics (global stats + user's stats)
		stats.Update(interaction.IsCommand)
		chat.Stats.Update(interaction.IsCommand)
	}

	// Run both limiters for successful requests
	spam.RunBothLimiters(chat, interaction.Tokens)

	return true
}
