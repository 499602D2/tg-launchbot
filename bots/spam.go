package bots

import (
	"context"
	"launchbot/users"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
	tb "gopkg.in/telebot.v3"
)

// TODO set-up as middleware in tb.Handle definitions in telegram.go
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

// Initialize the spam struct
func (spam *AntiSpam) Initialize() {
	// Create all maps
	spam.ChatBannedUntilTimestamp = make(map[*users.User]int64)
	spam.ChatLogs = make(map[*users.User]ChatLog)
	spam.ChatBanned = make(map[*users.User]bool)
	spam.Rules = make(map[string]int64)

	// Enforce a rate-limiter: 25 msg/sec, with 30 msg/sec bursts
	// TODO callbacks may have looser limits
	// TODO create a limiter wrapper with a method to perform a dual-limit,
	// that first limits the user and then the bot.
	spam.Limiter = rate.NewLimiter(25, 30)
}

func (spam *AntiSpam) UserLimiter(user *users.User, tokens int) {
	spam.Mutex.Lock()
	defer spam.Mutex.Unlock()

	chatLog := spam.ChatLogs[user]

	// If limiter doesn't exist, create it
	if chatLog.Limiter == nil {
		chatLog.Limiter = rate.NewLimiter(rate.Every(time.Second*3), 2)
	}

	// Reserve a limiter for this chat
	if chatLog.Limiter.AllowN(time.Now(), tokens) == false {
		log.Debug().Msg("User limiter returned false...")

		// If limiter returned false, wait until we can proceed
		err := chatLog.Limiter.WaitN(context.Background(), tokens)

		if err != nil {
			log.Error().Err(err).Msgf("Error using chat's Limiter.Wait()")
		}
	}

	// No spam, update chat's ConversionLog
	spam.ChatLogs[user] = chatLog
}

func (spam *AntiSpam) GlobalLimiter(tokens int) {
	spam.Mutex.Lock()
	defer spam.Mutex.Unlock()

	// Reserve a token from the main pool
	if spam.Limiter.AllowN(time.Now(), tokens) == false {
		log.Debug().Msg("Global limiter returned false...")

		// If limiter returned false, wait until we can proceed
		err := spam.Limiter.WaitN(context.Background(), tokens)

		if err != nil {
			log.Error().Err(err).Msgf("Error using global Limiter.Wait()")
		}
	}
}

func (spam *AntiSpam) RunBothLimiters(user *users.User, tokens int) {
	spam.GlobalLimiter(tokens)
	spam.UserLimiter(user, tokens)
}

// When user sends a command, verify the chat is eligible for a command parse.
func PreHandler(tg *TelegramBot, user *users.User, c tb.Context, tokens int) bool {
	if c.Chat().Type != tb.ChatPrivate {
		// If chat is not private, ensure sender is an admin
		member, err := tg.Bot.ChatMemberOf(c.Chat(), c.Sender())

		if err != nil {
			log.Warn().Msgf("Running pre-handler failed when requesting ChatMemberOf")

			handleTelegramError(c, err, tg)
			return false
		}

		// If user is not an admin, check if we can remove the message before returning
		// Alternatively, if chat permits anyone to send commands, parse it.
		if !user.AnyoneCanSendCommands && member.Role != tb.Administrator && member.Role != tb.Creator {
			// Get bot's member status
			botMember, err := tg.Bot.ChatMemberOf(c.Chat(), tg.Bot.Me)

			if err != nil {
				log.Error().Msg("Loading bot's permissions in chat failed")

				handleTelegramError(c, err, tg)
				return false
			}

			// If we have permission to delete messages, delete the command message
			if botMember.CanDeleteMessages {
				err = tg.Bot.Delete(c.Message())
			} else {
				// If no permission, delete the message
				log.Debug().Msgf("Cannot delete messages in chat=%d", c.Chat().ID)
				return false
			}

			// Check errors
			if err != nil {
				log.Error().Msg("Deleting message sent by a non-admin failed")
				handleTelegramError(c, err, tg)
				return false
			}

			log.Debug().Msgf("Deleted message by non-admin in chat=%d", c.Chat().ID)
			return false
		}
	}

	// Run limiters for global token pool + user's token pool
	log.Debug().Msgf("Taking %d token(s) from both pools...", tokens)
	tg.Spam.RunBothLimiters(user, tokens)

	return true
}
