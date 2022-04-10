package bots

import (
	"launchbot/users"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type AntiSpam struct {
	/* In-memory struct keeping track of banned chats and per-chat activity */
	ChatBanned               map[users.User]bool     // Simple "if ChatBanned[chat] { do }" checks
	ChatBannedUntilTimestamp map[users.User]int64    // How long banned chats are banned for
	ChatLogs                 map[users.User]*ChatLog // Map chat ID to a ChatLog struct
	Rules                    map[string]int64        // Arbitrary rules for code flexibility
	Mutex                    sync.Mutex              // Mutex to avoid concurrent map writes
}

type ChatLog struct {
	/* Per-chat struct keeping track of activity for spam management */
	NextAllowedCommandTimestamp int64 // Next time the chat is allowed to call a command
	CommandSpamOffenses         int   // Count of spam offences (not used)
}

/* Initialize the spam struct */
func (spam *AntiSpam) Initialize() {
	spam.ChatBannedUntilTimestamp = make(map[users.User]int64)
	spam.ChatLogs = make(map[users.User]*ChatLog)
	spam.ChatBanned = make(map[users.User]bool)
	spam.Rules = make(map[string]int64)
}

func CommandPreHandler(spam *AntiSpam, user *users.User, sentAt int64) bool {
	/* When user sends a command, verify the chat is eligible for a command parse. */
	spam.Mutex.Lock()
	chatLog := spam.ChatLogs[*user]

	if chatLog.NextAllowedCommandTimestamp > sentAt {
		chatLog.CommandSpamOffenses++
		spam.ChatLogs[*user] = chatLog
		spam.Mutex.Unlock()

		log.Info().Msgf("Chat %s:%d now has %d spam offenses", user.Platform, user.Id, chatLog.CommandSpamOffenses)
		return false
	}

	// No spam, update chat's ConversionLog
	chatLog.NextAllowedCommandTimestamp = time.Now().Unix() + spam.Rules["TimeBetweenCommands"]
	spam.ChatLogs[*user] = chatLog
	spam.Mutex.Unlock()
	return true
}
