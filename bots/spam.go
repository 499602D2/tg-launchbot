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
	BroadcastLimiter         *rate.Limiter                 // Main rate-limiter
	ChatLimiters             map[*users.User]*rate.Limiter // Map chat ID to a per-chat limiter
	NotificationSendUnderway bool                          // True if notifications are currently being sent
	VerboseLog               bool                          // Toggle to enable verbose permission logging
	Mutex                    sync.Mutex                    // Mutex to avoid concurrent map writes
}

// An interaction handled by preHandler
type Interaction struct {
	IsAdminOnly    bool   // Only set to true for administrative commands
	IsCommand      bool   // Is this interaction a command?
	IsPermissioned bool   // If interaction originates from group or channel
	AnyoneCanUse   bool   // An interaction that is one-off (message expansions)
	CallerIsAdmin  bool   // Is the caller an admin?
	Name           string // Name of command or callback
	CbData         string // Callback data for more accurate logging
	Tokens         int    // Token-count this call requires
}

// Initialize the spam struct
func (spam *Spam) Initialize(broadcastLimit int, broadcastBurst int) {
	// Create maps for the spam struct
	spam.ChatLimiters = make(map[*users.User]*rate.Limiter)

	// Enforce a global rate-limiter, at 20 msg/sec + burst capacity of 5 msg/sec
	spam.BroadcastLimiter = rate.NewLimiter(rate.Limit(broadcastLimit), broadcastBurst)

	if broadcastLimit > 30 || broadcastLimit+broadcastBurst > 30 {
		log.Warn().Msgf(
			"Very high broadcast limits (%d, %d): consider lowering the limits. "+
				"Ideally, broadcastLimit < 30 and broadcastLimit+broadcastBurst <= 30",
			broadcastLimit, broadcastBurst)
	}
}

// Enforce a per-chat rate-limiter
func (spam *Spam) UserLimiter(chat *users.User, stats *stats.Statistics, tokens int) {
	if spam.ChatLimiters[chat] == nil {
		// If limiter doesn't exist, create it
		spam.Mutex.Lock()

		/* Create a limiter: 1 msg every 3 seconds, plus 2 msg/sec burst capacity.
		https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this

		"Also note that your bot will not be able to send more than 20 messages
		per minute to the same group." */
		spam.ChatLimiters[chat] = rate.NewLimiter(rate.Every(time.Second*3), 2)
		spam.Mutex.Unlock()
	}

	// Log limit start
	start := time.Now()

	// Wait until we can take as many tokens as we need
	err := spam.ChatLimiters[chat].WaitN(context.Background(), tokens)

	// Track enforced limits
	duration := time.Since(start)
	stats.LimitsEnforced++

	// FUTURE track means with a fixed-length set of rate-limit durations (average same-index insertions)
	if stats.LimitsEnforced == 1 {
		stats.LimitsAverage = duration.Seconds()
	} else {
		/* If duration is lower than the average, the average drops. And, if
		the duration is greater than the average, the average increases. */
		stats.LimitsAverage = stats.LimitsAverage + (duration.Seconds()-stats.LimitsAverage)/float64(stats.LimitsEnforced)
	}

	if err != nil {
		log.Error().Err(err).Msgf("Error using chat's Limiter.WaitN()")
	}
}

// Enforces a global broadcast rate-limiter for the bot. Telegram has some hard
// rate-limits, including a 30 messages-per-second send limit for messages
// under 512 bytes.
func (spam *Spam) GlobalLimiter(tokens int) {
	// Take the required amount of tokens, sleep if required
	err := spam.BroadcastLimiter.WaitN(context.Background(), tokens)

	if err != nil {
		log.Error().Err(err).Msgf("Error using global Limiter.WaitN()")
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
	// Update time chat was last active
	chat.LastActive = time.Now()
	chat.LastActivityType = users.Interaction

	if interaction.IsPermissioned {
		// In groups, we need to ensure that regular users cannot do funny things
		if interaction.IsAdminOnly || !(chat.AnyoneCanSendCommands || interaction.AnyoneCanUse) {
			// Admin-only interaction, or group doesn't allow users to interact with the bot
			if !interaction.CallerIsAdmin {
				if interaction.IsCommand {
					/* Run the global limiter for message deletions, as they're trivial to spam.
					Callbacks show an alert, which slows users down enough. */
					if spam.NotificationSendUnderway {
						/* If notifications are being sent, rate-limit removals more heavily.
						This helps us avoid a scenario where notifications are being sent, but the
						token pool is being drained by non-admins spamming messages that are being
						removed. */
						log.Warn().Msg("Notification send underway: rate-limiting message removal")
						spam.UserLimiter(chat, stats, 1)
					}

					spam.GlobalLimiter(1)
				}

				if spam.VerboseLog {
					log.Debug().Msgf("[Pre-handler] Not allowing interaction=%s/%s in chat=%s",
						interaction.Name, interaction.CbData, chat.Id)
				}

				return false
			}
		}
	}

	// Ensure command counter cannot be iterated by spamming the stats refresh button
	unloggedInteractions := map[string]bool{
		"stats": true, "generic": true,
	}

	// Check if command should not be logged
	_, shouldNotBeLogged := unloggedInteractions[interaction.Name]

	if !shouldNotBeLogged || interaction.IsCommand {
		// Processing allowed: save statistics
		stats.Update(interaction.IsCommand)
		chat.Stats.Update(interaction.IsCommand)
	}

	// Run both limiters for successful requests
	spam.RunBothLimiters(chat, interaction.Tokens, stats)

	if spam.VerboseLog {
		log.Debug().Msgf("[Pre-handler] Allowed interaction=%s/%s in chat=%s",
			interaction.Name, interaction.CbData, chat.Id)
	}

	return true
}
