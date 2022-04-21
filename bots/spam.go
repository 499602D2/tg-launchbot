package bots

import (
	"context"
	"launchbot/users"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// TODO set-up as middleware in tb.Handle definitions in telegram.go
type AntiSpam struct {
	/* In-memory struct keeping track of banned chats and per-chat activity */
	ChatBanned               map[users.User]bool    // Simple "if ChatBanned[chat] { do }" checks
	ChatBannedUntilTimestamp map[users.User]int64   // How long banned chats are banned for
	ChatLogs                 map[users.User]ChatLog // Map chat ID to a ChatLog struct
	Rules                    map[string]int64       // Arbitrary rules for code flexibility
	Limiter                  *rate.Limiter          // Main rate-limiter
	Mutex                    sync.Mutex             // Mutex to avoid concurrent map writes
}

/* Per-chat struct keeping track of activity for spam management */
type ChatLog struct {
	/* TODO track messages sent to group as a trailing-minute array of timestamps

	(No more timing out when users spam callbacks)
	https://telegra.ph/So-your-bot-is-rate-limited-01-26
	*/
	NextAllowedCommandTimestamp int64         // Next time the chat is allowed to call a command
	CommandSpamOffenses         int           // Count of spam offences (not used)
	Limiter                     *rate.Limiter // Per-chat ratelimiter
}

/* Initialize the spam struct */
func (spam *AntiSpam) Initialize() {
	spam.ChatBannedUntilTimestamp = make(map[users.User]int64)
	spam.ChatLogs = make(map[users.User]ChatLog)
	spam.ChatBanned = make(map[users.User]bool)
	spam.Rules = make(map[string]int64)

	// Enforce a rate-limiter: 25 msg/sec, with 30 msg/sec bursts
	// TODO callbacks may have looser limits
	// TODO create a limiter wrapper with a method to perform a dual-limit,
	// that first limits the user and then the bot.
	spam.Limiter = rate.NewLimiter(25, 30)
}

/* When user sends a command, verify the chat is eligible for a command parse. */
func PreHandler(tg *TelegramBot, user *users.User, sentAt int64) bool {
	tg.Spam.Mutex.Lock()
	chatLog := tg.Spam.ChatLogs[*user]

	// If limiter doesn't exist, create it
	if chatLog.Limiter == nil {
		// 20 msg/minute limit -> every 3 seconds
		chatLog.Limiter = rate.NewLimiter(rate.Every(time.Second*3), 2)
	}

	// Run limiter
	if chatLog.Limiter.Allow() == false {
		log.Debug().Msg("limiter.Allow() returned false...")

		// Dummy context
		ctx := context.Background()

		// If limiter returned false, wait until we can proceed
		err := chatLog.Limiter.Wait(ctx)

		if err != nil {
			log.Error().Err(err).Msgf("Error using Limiter.Wait()")
		}
	}

	/*if chatLog.NextAllowedCommandTimestamp > sentAt {
		chatLog.CommandSpamOffenses++
		spam.ChatLogs[*user] = chatLog
		spam.Mutex.Unlock()

		log.Info().Msgf("Chat %s:%d now has %d spam offenses", user.Platform, user.Id, chatLog.CommandSpamOffenses)
		return false
	}*/

	// No spam, update chat's ConversionLog
	// chatLog.NextAllowedCommandTimestamp = time.Now().Unix() + spam.Rules["TimeBetweenCommands"]
	tg.Spam.ChatLogs[*user] = chatLog
	tg.Spam.Mutex.Unlock()

	// Bump stats
	user.Stats.SentCommands++

	// Save stats
	// TODO save automatically whenever chat cache is cleaned
	go tg.Db.SaveUser(user)

	return true
}
