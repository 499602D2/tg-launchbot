package bots

import (
	"launchbot/users"
	"sync"

	"github.com/rs/zerolog/log"
	uuid "github.com/satori/go.uuid"
	tb "gopkg.in/telebot.v3"
)

/* A sendable implements a "generic" type of a sendable object.
This can be a notification or a command reply. These have a priority,
according to which they will be sent. */
type Sendable struct {
	/* Priority singifies the importance of this message (0 to 3).
	By default, sendables should be prioritized in the following order:
	0 (backburner): old message removal, etc.
	1 (not important): scheduled notifications
	2 (more important): replies to commands
	3 (send immediately): bot added to a new chat, telegram callbacks
	*/
	Priority int8

	/* Type, in ("remove", "notification", "command", "callback")
	Ideally, the sendable will go through a type-switch, according to which
	the correct execution will be performed. */
	Type string

	Message    *Message        // Message (may be empty)
	Recipients *users.UserList // Recipients of this sendable
	RateLimit  float32         // Ratelimits this sendable should obey
}

// The message content of a sendable
type Message struct {
	TextContent *string
	SendOptions tb.SendOptions
}

/* A queue of Telegram messages to be sent
- How should notifications work?
-
*/
type Queue struct {
	MessagesPerSecond float32              // Messages-per-second limit
	Messages          map[string]*Sendable // Queue of sendables (uniqueHash:sendable)
	Mutex             sync.Mutex           // Mutex to avoid concurrent writes
}

/* Implement a prioritized, ordered queue: if a new entry pops up, switch sendable temporarily...? */

/* TODO TODO TODO
- implement a more generic TelegramMessage format for pushing Telegram methods,
	i.e. in the case where we remove thousands of notifications at once.
*/

/* Adds a message to the Telegram message queue */
func (queue *Queue) Enqueue(sendable *Sendable, tg *TelegramBot, highPriority bool) {
	// Unique ID for this sendable
	uuid := uuid.NewV4().String()

	if highPriority {
		tg.HighPriority.Mutex.Lock()
		tg.HighPriority.HasItemsInQueue = true
		tg.HighPriority.Queue = append(tg.HighPriority.Queue, sendable)
		tg.HighPriority.Mutex.Unlock()
	} else {
		queue.Mutex.Lock()

		// Assign a random hash to the sendable, enqueue it
		queue.Messages[uuid] = sendable

		queue.Mutex.Unlock()
	}
}

func (sendable *Sendable) Send() {
	/*
		Simply add the Notification object to a sendQueue
		- ..?
	*/
	// Loop over the users, distribute into appropriate send queues
	switch sendable.Recipients.Platform {
	case "tg":
		log.Warn().Msg("Telegram message sender not implemented!")
	case "dg":
		log.Warn().Msg("Discord message sender not implemented!")
	}
}
