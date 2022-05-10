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

// When user sends a command, verify the chat is eligible for a command parse.
func PreHandler(tg *TelegramBot, user *users.User, c tb.Context) bool {
	if c.Chat().Type != tb.ChatPrivate {
		// If chat is not private, ensure sender is an admin
		member, err := tg.Bot.ChatMemberOf(c.Chat(), c.Sender())

		if err != nil {
			handleTelegramError(nil, err, tg)
		}

		if member.Role != tb.Administrator && member.Role != tb.Creator {
			return false
		}
	}

	// TODO handle admin checks
	// TODO users should be chat IDs, not users (verify when calling preHandler)

	tg.Spam.Mutex.Lock()
	chatLog := tg.Spam.ChatLogs[user]

	// If limiter doesn't exist, create it
	if chatLog.Limiter == nil {
		chatLog.Limiter = rate.NewLimiter(rate.Every(time.Second*3), 2)
	}

	// Reserve a token from the main pool
	if tg.Spam.Limiter.Allow() == false {
		log.Debug().Msg("Global limiter returned false...")

		// If limiter returned false, wait until we can proceed
		err := chatLog.Limiter.Wait(context.Background())

		if err != nil {
			log.Error().Err(err).Msgf("Error using global Limiter.Wait()")
		}
	}

	// Reserve a limiter for this chat
	if chatLog.Limiter.Allow() == false {
		log.Debug().Msg("User limiter returned false...")

		// If limiter returned false, wait until we can proceed
		err := chatLog.Limiter.Wait(context.Background())

		if err != nil {
			log.Error().Err(err).Msgf("Error using chat's Limiter.Wait()")
		}
	}

	// No spam, update chat's ConversionLog
	tg.Spam.ChatLogs[user] = chatLog
	tg.Spam.Mutex.Unlock()

	// Bump stats
	user.Stats.SentCommands++

	// Save stats
	// TODO save automatically whenever chat cache is cleaned
	go tg.Db.SaveUser(user)

	return true
}
