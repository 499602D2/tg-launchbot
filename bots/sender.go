package bots

import (
	"launchbot/users"
	"sync"
	"time"

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
	3 (send immediately): bot added to a new chat, telegram callbacks */
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

/* A queue of sendables to be sent */
type Queue struct {
	MessagesPerSecond float32              // Messages-per-second limit
	Sendables         map[string]*Sendable // Queue of sendables (uniqueHash:sendable)
	Mutex             sync.Mutex           // Mutex to avoid concurrent writes
}

/* Adds a message to the Telegram message queue */
func (queue *Queue) Enqueue(sendable *Sendable, tg *TelegramBot, highPriority bool) {
	// Unique ID for this sendable
	uuid := uuid.NewV4().String()

	// If sendable is high-priority, add it to the high-priority queue
	if highPriority {
		tg.HighPriority.Mutex.Lock()

		// Mark queue as having items, append sendable to queue
		tg.HighPriority.HasItemsInQueue = true
		tg.HighPriority.Queue = append(tg.HighPriority.Queue, sendable)

		tg.HighPriority.Mutex.Unlock()
		return
	}

	// Assign a random hash to the sendable, enqueue it
	queue.Mutex.Lock()
	queue.Sendables[uuid] = sendable
	queue.Mutex.Unlock()
}

/* Switches according to the recipient platform and the sendable type. */
func (sendable *Sendable) Send() {
	// Loop over the users, distribute into appropriate send queues
	switch sendable.Recipients.Platform {
	case "tg":
		log.Warn().Msg("Telegram message sender not implemented!")
	case "dg":
		log.Warn().Msg("Discord message sender not implemented!")
	}
}

/* HighPrioritySender sends singular high-priority messages. */
func highPrioritySender(tg *TelegramBot, message *Message, user *users.User) bool {
	// TODO: use sendable.Send()
	_, err := tg.Bot.Send(tb.ChatID(user.Id),
		*message.TextContent, &message.SendOptions,
	)

	if err != nil {
		if !handleTelegramError(err) {
			// If error is non-recoverable, continue the loop
			log.Warn().Msg("Non-recoverable error in high-priority sender")
			return false
		} else {
			// Error is recoverable: try sending again
			// TelegramMessageSender(...)
			log.Warn().Msg("NOT IMPLEMENTED: message re-try after recoverable error in high-priority sender")
		}
	}

	return true
}

/* Clears the priority queue. */
func clearPriorityQueue(tg *TelegramBot, sleep bool) {
	// Lock the high-priority queue
	tg.HighPriority.Mutex.Lock()

	// TODO: sort before looping over (according to priority)
	for _, prioritySendable := range tg.HighPriority.Queue {
		for _, priorityUser := range prioritySendable.Recipients.Users {
			log.Info().Msgf("Sending high-priority sendable for %s:%d",
				priorityUser.Platform, priorityUser.Id,
			)

			// Loop over users, send high-priority message
			highPrioritySender(tg, prioritySendable.Message, priorityUser)

			// Stay within limits if needed
			if sleep || len(prioritySendable.Recipients.Users) > 1 {
				// TODO use a sleeper function that implements back-off and re-try
				time.Sleep(time.Millisecond * time.Duration(1.0/prioritySendable.RateLimit*1000.0))
			}
		}
	}

	log.Debug().Msg("High-priority message queue cleared")

	// Reset high-priority queue
	tg.HighPriority.HasItemsInQueue = false
	tg.HighPriority.Queue = []*Sendable{}

	// Unlock high-priority queue
	tg.HighPriority.Mutex.Unlock()
}

/* TelegramSender is a daemon-like function that listens to the notification
and priority queues for incoming messages and notifications. */
func TelegramSender(tg *TelegramBot) {
	const (
		priorityQueueClearInterval = 3
	)

	for {
		// Check notification queue
		if len(tg.Queue.Sendables) != 0 {
			tg.Queue.Mutex.Lock()

			for hash, sendable := range tg.Queue.Sendables {
				log.Info().Msgf("Sending sendable with hash=%s", hash)
				sentIds := make(map[users.User]int)

				var i uint32
				for i, user := range sendable.Recipients.Users {
					// TODO: use sendable.Send()
					sent, err := tg.Bot.Send(tb.ChatID(user.Id),
						*sendable.Message.TextContent, &sendable.Message.SendOptions,
					)

					if err != nil {
						if !handleTelegramError(err) {
							// If error is non-recoverable, continue the loop
							log.Warn().Msg("Non-recoverable error in sender, continuing loop")
							delete(tg.Queue.Sendables, hash)
							continue
						} else {
							// Error is recoverable: try sending again
							// TODO re-try (TelegramMessageSender(...))
							log.Warn().Msg("NOT IMPLEMENTED: message re-try after recoverable error (e.g. timeout)")
						}
					} else {
						// Successfully sent; store sent notification's ID for later use
						delete(tg.Queue.Sendables, hash)
						if sendable.Type == "notification" {
							sentIds[*user] = sent.ID
						}
					}

					// Sleep long enough to stay within API limits: convert messagesPerSecond to ms
					if i < len(sendable.Recipients.Users)-1 {
						// TODO: replace with a sleep function, with fail counter, back-off, etc.
						time.Sleep(time.Millisecond * time.Duration(1.0/sendable.RateLimit*1000.0))
					}

					/* Periodically, during long sends, check if the TelegramBot.PriorityQueued is set.
					This flag is enabled if there is one, or more, enqueued high-priority messages
					in the high-priority queue. Vary the priorityQueueClearInterval variable to tune
					how often to check for pending messages.

					The justification for this is the fact that the main queue's mutex is locked when
					the sending process is started, and could be locked for minutes. This alleviates
					the issue of messages sitting in the queue for ages, sacrificing the send time of
					mass-notifications for timely responses to e.g. callback queries and commands. */
					if tg.HighPriority.HasItemsInQueue && i%priorityQueueClearInterval == 0 {
						log.Debug().Msg("High-priority messages in queue during long send")
						clearPriorityQueue(tg, true)
					}
				}

				// db.SaveSentNotificationIds()
				if sendable.Type == "notification" {
					log.Warn().Msg("Not implemented: sent notification IDs will not be saved!")
				}

				// Send done, log
				log.Info().Msgf("Sent %d notifications for sendable=%s", i, hash)
			}

			tg.Queue.Mutex.Unlock()
		}

		// Check if priority queue is populated (and skip sleeping if one entry)
		if tg.HighPriority.HasItemsInQueue {
			log.Debug().Msg("High-priority messages in queue")
			clearPriorityQueue(tg, false)
		}

		// Clear queue every 250 ms
		time.Sleep(time.Duration(time.Millisecond * 100))
	}
}
