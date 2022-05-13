package bots

import (
	"launchbot/sendables"
	"launchbot/users"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	uuid "github.com/satori/go.uuid"
	tb "gopkg.in/telebot.v3"
)

// A queue of sendables to be sent
type Queue struct {
	MessagesPerSecond float32                        // Messages-per-second limit
	Sendables         map[string]*sendables.Sendable // Queue of sendables (uniqueHash:sendable)
	Mutex             sync.Mutex                     // Mutex to avoid concurrent writes
}

// Adds a message to the Telegram message queue
func (queue *Queue) Enqueue(sendable *sendables.Sendable, tg *TelegramBot, highPriority bool) {
	// Unique ID for this sendable
	uuid := uuid.NewV4().String()

	// Calculate size and set token count
	sendableSize := sendable.PerceivedByteSize()
	if sendableSize >= 512 && !highPriority {
		sendable.Tokens = 6
		log.Debug().Msgf("Reserved %d token(s) for sendable, size=%d", sendable.Tokens, sendableSize)
	} else {
		sendable.Tokens = 1
	}

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

// HighPrioritySender sends singular high-priority messages.
func highPrioritySender(tg *TelegramBot, message *sendables.Message, user *users.User) bool {
	// If message needs to have its time set properly, do it now
	text := message.TextContent

	if message.AddUserTime {
		text = sendables.SetTime(*text, user, message.RefTime, true)
	}

	// TODO store integer-id
	id, _ := strconv.Atoi(user.Id)

	// TODO: use sendable.Send()
	sent, err := tg.Bot.Send(tb.ChatID(id),
		*text, &message.SendOptions,
	)

	if err != nil {
		if !handleSendError(sent, err, tg) {
			// If error is non-recoverable, continue the loop
			log.Warn().Msg("Unrecoverable error in high-priority sender")
			return false
		} else {
			// Error is recoverable: try sending again
			// TelegramMessageSender(...)
			log.Warn().Msg("NOT IMPLEMENTED: message re-try after recoverable error in high-priority sender")
		}
	}

	return true
}

// Clears the priority queue.
func clearPriorityQueue(tg *TelegramBot) {
	// Lock the high-priority queue
	tg.HighPriority.Mutex.Lock()

	// TODO: sort before looping over (according to priority)
	for _, prioritySendable := range tg.HighPriority.Queue {
		for _, priorityUser := range prioritySendable.Recipients.Users {
			// Loop over users, send high-priority message
			highPrioritySender(tg, prioritySendable.Message, priorityUser)
		}
	}

	//log.Debug().Msg("High-priority message queue cleared")

	// Reset high-priority queue
	tg.HighPriority.HasItemsInQueue = false
	tg.HighPriority.Queue = []*sendables.Sendable{}

	// Unlock high-priority queue
	tg.HighPriority.Mutex.Unlock()
}

// TelegramSender is a daemon-like function that listens to the notification
// and priority queues for incoming messages and notifications.

// TODO: alternatively use a job queue + dispatcher (V3.1+)
// - should feature priority tags

// Alternatively: implement Sendables so that they are unwrapped into distinct
// objects, that can then be queued. Then, implement a simple, linked priority-
// weighed queue and a dequeuer + all the related queue/dequeue/insert methods.

// This could replace the priority queue, as the head of the queue would be the
// one that's always deleted.

// - Mutexes? Constant locking + unlocking (which may be fine)
func TelegramSender(tg *TelegramBot) {
	const (
		priorityQueueClearInterval = 3
	)

	for {
		// Check notification queue
		if len(tg.Queue.Sendables) != 0 {
			tg.Queue.Mutex.Lock()

			for hash, sendable := range tg.Queue.Sendables {
				// Keep track of sent IDs
				sentIds := make(map[users.User]int)

				// Keep pointer, simplifies handling if time needs to be set in message
				defaultText := sendable.Message.TextContent
				var text *string

				// Calculate how many tokens each message requires
				if sendable.PerceivedByteSize() >= 512 {
					log.Warn().Msgf("Sendable is more than 512 bytes long, taking 6 tokens per send")
				}

				log.Info().Msgf("Sending sendable with hash=%s", hash)

				var i uint32
				for i, user := range sendable.Recipients.Users {
					// Rate-limiter: check if we have tokens to proceed
					tg.Spam.GlobalLimiter(sendable.Tokens)

					// If message needs to have its time set properly, do it now
					if sendable.Message.AddUserTime {
						text = sendables.SetTime(*defaultText, user, sendable.Message.RefTime, true)
					} else {
						text = defaultText
					}

					// TODO: use sendable.Send()
					// Use the tb.Sendable interface?
					// https://pkg.go.dev/gopkg.in/telebot.v3@v3.0.0?utm_source=gopls#Sendable
					id, _ := strconv.Atoi(user.Id)
					sent, err := tg.Bot.Send(tb.ChatID(id), *text, &sendable.Message.SendOptions)

					if err != nil {
						if !handleSendError(sent, err, tg) {
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

					/*
						Periodically, during long sends, check if the TelegramBot.PriorityQueued is set.
						This flag is enabled if there is one, or more, enqueued high-priority messages
						in the high-priority queue. Vary the priorityQueueClearInterval variable to tune
						how often to check for pending messages.

						The justification for this is the fact that the main queue's mutex is locked when
						the sending process is started, and could be locked for minutes. This alleviates
						the issue of messages sitting in the queue for ages, sacrificing the send time of
						mass-notifications for timely responses to e.g. callback queries and commands.
					*/
					if (tg.HighPriority.HasItemsInQueue) && (i%priorityQueueClearInterval == 0) {
						log.Debug().Msg("High-priority messages in queue during long send")
						clearPriorityQueue(tg)
					}
				}

				// db.SaveSentNotificationIds()
				if sendable.Type == "notification" {
					// TODO save notification IDs to database

					// Save statistics
					tg.Stats.Notifications += len(sentIds)
				}

				// Send done, log
				log.Info().Msgf("Sent %d notification(s) for sendable=%s", i+1, hash)
			}

			tg.Queue.Mutex.Unlock()
		}

		// Check if priority queue is populated (and skip sleeping if one entry)
		if tg.HighPriority.HasItemsInQueue {
			//log.Debug().Msg("High-priority messages in queue")
			clearPriorityQueue(tg)
		}

		// Clear queue every 50 ms
		time.Sleep(time.Duration(time.Millisecond * 50))
	}
}
