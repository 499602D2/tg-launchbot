package bots

import (
	"context"
	"launchbot/stats"
	"launchbot/users"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// In-memory struct keeping track of banned chats and per-chat activity
type Spam struct {
	Chats   map[*users.User]Chat // Map chat ID to a per-chat spam struct
	Rules   map[string]int64     // Arbitrary rules for code flexibility
	Limiter *rate.Limiter        // Main rate-limiter
	Mutex   sync.Mutex           // Mutex to avoid concurrent map writes
}

// Per-chat struct keeping track of activity for spam management
type Chat struct {
	Limiter *rate.Limiter // Per-chat ratelimiter
}

// An interaction handled by preHandler
type Interaction struct {
	IsAdminOnly   bool
	IsCommand     bool
	IsGroup       bool   // Called from a group?
	CallerIsAdmin bool   // Is the caller an admin?
	Name          string // Name of command or callback
	Tokens        int    // Token-count this call requires
}

// Initialize the spam struct
func (spam *Spam) Initialize() {
	// Create maps for the spam struct
	spam.Chats = make(map[*users.User]Chat)
	spam.Rules = make(map[string]int64)

	// Enforce a global rate-limiter: sustain 25 msg/sec, with 30 msg/sec bursts
	// A change of 5 msg/sec is 300 more messages in a minute (!)
	spam.Limiter = rate.NewLimiter(25, 30)
}

// Enforce a per-chat rate-limiter. Typically, roughly 20 messages per minute
// can be sent to one chat.
func (spam *Spam) UserLimiter(user *users.User, stats *stats.Statistics, tokens int) {
	// If limiter doesn't exist, create it
	if spam.Chats[user].Limiter == nil {
		spam.Mutex.Lock()

		// Load user's chatLog, assign new limiter, save back
		chatLog := spam.Chats[user]
		chatLog.Limiter = rate.NewLimiter(rate.Every(time.Second*3), 2)
		spam.Chats[user] = chatLog

		spam.Mutex.Unlock()
	}

	// Log limit start
	start := time.Now()

	// Wait until we can take as many tokens as we need
	err := spam.Chats[user].Limiter.WaitN(context.Background(), tokens)

	// Track enforced limits
	duration := int64(time.Since(start))
	stats.LimitsEnforced++

	if stats.LimitsEnforced == 1 {
		stats.LimitsAverage = duration
	} else {
		stats.LimitsAverage = (stats.LimitsAverage) + (duration-stats.LimitsAverage)/stats.LimitsEnforced
	}

	if err != nil {
		log.Error().Err(err).Msgf("Error using chat's Limiter.Wait()")
	}
}

// Enforces a global rate-limiter for the bot. Telegram has some hard rate-limits,
// including a 30 messages-per-second send limit for messages under 512 bytes.
func (spam *Spam) GlobalLimiter(tokens int) {
	// Take the required amount of tokens, sleep if required
	err := spam.Limiter.WaitN(context.Background(), tokens)

	if err != nil {
		log.Error().Err(err).Msgf("Error using global Limiter.Wait()")
	}
}

// A wrapper method to run both the global and user limiter at once
func (spam *Spam) RunBothLimiters(user *users.User, tokens int, stats *stats.Statistics) {
	// The user-limiter is ran first, as it's far more restricting
	spam.UserLimiter(user, stats, tokens)
	spam.GlobalLimiter(tokens)
}

// When an interaction is received, ensure the sender is qualified for it
func (spam *Spam) PreHandler(interaction *Interaction, chat *users.User, stats *stats.Statistics) bool {
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
	spam.RunBothLimiters(chat, interaction.Tokens, stats)

	return true
}
