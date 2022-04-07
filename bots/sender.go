package bots

import (
	"launchbot/users"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

/* HighPrioritySender sends singular high-priority messages. */
func highPrioritySender(tg *TelegramBot, message *Message, user *users.User) bool {
	_, err := tg.Bot.Send(
		tb.ChatID(int64(user.Id)),
		*message.TextContent,
		&message.SendOptions,
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

func clearPriorityQueue(tg *TelegramBot) {
	// Lock the high-priority queue
	tg.HighPriority.Mutex.Lock()
	for _, prioritySendable := range tg.HighPriority.Queue {
		for n, priorityUser := range prioritySendable.Recipients.Users {
			log.Info().Msgf("Sending high-priority sendable for %s:%d",
				priorityUser.Platform, priorityUser.Id,
			)

			// Loop over users, send high-priority message
			highPrioritySender(tg, prioritySendable.Message, priorityUser)

			if n < len(prioritySendable.Recipients.Users)-1 {
				time.Sleep(time.Millisecond * time.Duration(1.0/prioritySendable.RateLimit*1000.0))
			}
		}
	}

	log.Info().Msg("High-priority message queue cleared")

	// Reset and unlock high-priority queue, lock main queue
	tg.HighPriority.HasItemsInQueue = false
	tg.HighPriority.Queue = []*Sendable{}
	tg.HighPriority.Mutex.Unlock()
}

/* TelegramSender is a daemon-like function that listens to the notification
and priority queues for incoming messages and notifications. */
func TelegramSender(tg *TelegramBot) {
	/* TODO:
	- use exponential back-off: https://en.wikipedia.org/wiki/Exponential_backoff

	- lock notificationSender in session until sent (disallow overlapping sends)

	- use a generic sendable object instead of a notification
		- send e.g. delete requests fast
		- how ratelimited?
		- user Go-generics
			- add a type field to the sendable objects...?
	*/

	for {
		// Check notification queue
		if len(tg.MessageQueue.Messages) != 0 {
			tg.MessageQueue.Mutex.Lock()

			for hash, sendable := range tg.MessageQueue.Messages {
				log.Info().Msgf("Sending sendable with hash=%s", hash)
				sentIds := make(map[users.User]int)

				for i, user := range sendable.Recipients.Users {
					// Send message
					sent, err := tg.Bot.Send(
						tb.ChatID(int64(user.Id)),
						*sendable.Message.TextContent,
						&sendable.Message.SendOptions,
					)

					if err != nil {
						if !handleTelegramError(err) {
							// If error is non-recoverable, continue the loop
							log.Warn().Msg("Non-recoverable error in sender, continuing loop")
							delete(tg.MessageQueue.Messages, hash)
							continue
						} else {
							// Error is recoverable: try sending again
							// TelegramMessageSender(...)
							log.Warn().Msg("NOT IMPLEMENTED: message re-try after recoverable error (e.g. timeout)")
						}
					} else {
						// Successfully sent; store sent notification's ID for later use
						delete(tg.MessageQueue.Messages, hash)
						if sendable.Type == "notification" {
							sentIds[*user] = sent.ID
						}
					}

					// Sleep long enough to stay within API limits: convert messagesPerSecond to ms
					if i < len(sendable.Recipients.Users)-1 {
						time.Sleep(time.Millisecond * time.Duration(1.0/sendable.RateLimit*1000.0))
					}

					/* Periodically, during long sends, check if the TelegramBot.PriorityQueued is set.
					This flag is enabled if there is one, or more, enqueued high-priority messages
					in the high-priority queue. */
					if tg.HighPriority.HasItemsInQueue {
						log.Info().Msg("High-priority messages in queue during long send")
						clearPriorityQueue(tg)
					}
				}

				// db.SaveSentNotificationIds()
				if sendable.Type == "notification" {
					log.Warn().Msg("Not implemented: sent notification IDs will not be saved!")
				}
			}

			// TODO: allow re-unlock if high priority sendable enqueued
			tg.MessageQueue.Mutex.Unlock()
		}

		// Check if priority queue is populated
		if tg.HighPriority.HasItemsInQueue {
			log.Info().Msg("High-priority messages in queue")
			clearPriorityQueue(tg)
		}

		// Clear queue every 250 ms
		time.Sleep(time.Duration(time.Millisecond * 250))
	}
}
