package telegram

import (
	"errors"
	"fmt"
	"launchbot/sendables"
	"launchbot/users"
	"strconv"
	"time"

	"github.com/hako/durafmt"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// HighPrioritySender sends singular high-priority messages.
func highPrioritySender(tg *Bot, message *sendables.Message, chat *users.User) bool {
	// If message needs to have its time set properly, do it now
	text := message.TextContent

	if message.AddUserTime {
		text = sendables.SetTime(text, chat, message.RefTime, true, true, false)
	}

	id, _ := strconv.Atoi(chat.Id)

	// FUTURE: use sendable.Send()
	sent, err := tg.Bot.Send(tb.ChatID(id), text, &message.SendOptions)

	if err != nil {
		if !tg.handleError(nil, sent, err, int64(id)) {
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
func clearPriorityQueue(tg *Bot) {
	// Lock the high-priority queue
	tg.Queue.HighPriority.Mutex.Lock()

	// FUTURE: sort before looping over (according to priority)
	for _, prioritySendable := range tg.Queue.HighPriority.Queue {
		for _, priorityUser := range prioritySendable.Recipients {
			// Loop over users, send high-priority message
			highPrioritySender(tg, prioritySendable.Message, priorityUser)
		}
	}

	// Reset high-priority queue
	tg.Queue.HighPriority.HasItemsInQueue = false
	tg.Queue.HighPriority.Queue = []*sendables.Sendable{}

	// Unlock high-priority queue
	tg.Queue.HighPriority.Mutex.Unlock()
}

// Delete a Telegram message with a chat ID and a message ID
func DeleteNotificationMessage(tg *Bot, sendable *sendables.Sendable, user *users.User) {
	// Load ID pair
	msgId, ok := sendable.MessageIDs[user.Id]

	if !ok {
		log.Warn().Msgf("Unable to find message ID during deletion for user=%s", user.Id)
		return
	}

	// Convert chat ID to integer
	chatId, err := strconv.Atoi(user.Id)

	if err != nil {
		log.Error().Err(err).Msgf("Forming chat ID failed while removing sent messages")
		return
	}

	// Convert message ID to an integer
	messageId, err := strconv.Atoi(msgId)

	if err != nil {
		log.Error().Err(err).Msgf("Forming message ID failed while removing sent messages")
		return
	}

	// Build tb.Message, and delete it
	messageToBeDeleted := &tb.Message{ID: messageId, Chat: &tb.Chat{ID: int64(chatId)}}
	err = tg.Bot.Delete(messageToBeDeleted)

	if err != nil {
		tg.handleError(nil, messageToBeDeleted, err, int64(chatId))
		log.Error().Err(err).Msgf("Deleting message %s:%s failed", user.Id, msgId)
	}
}

// Send a notification
func SendNotification(tg *Bot, sendable *sendables.Sendable, user *users.User) (string, bool) {
	// Convert id to an integer
	id, _ := strconv.Atoi(user.Id)

	var text string
	if sendable.Message.AddUserTime {
		text = sendables.SetTime(sendable.Message.TextContent, user, sendable.Message.RefTime, true, false, false)
	} else {
		text = sendable.Message.TextContent
	}

	// Send message
	sent, err := tg.Bot.Send(tb.ChatID(id), text, &sendable.Message.SendOptions)

	if err != nil {
		var floodErr tb.FloodError

		// If error is a rate-limit message, add one token
		if errors.As(err, &floodErr) {
			log.Warn().Err(err).Msgf("Received a tb.FloodError (retryAfter=%d): adding one token (tokens=%d+1)",
				floodErr.RetryAfter, sendable.Tokens)

			if sendable.Tokens < 6 {
				sendable.Tokens++
			}
		}

		if !tg.handleError(nil, sent, err, int64(id)) {
			log.Warn().Msg("Non-recoverable error in sender, continuing loop")
			return "", false
		}

		// TODO Error is recoverable: try sending again SendNotification(...)
		log.Error().Err(err).Msg("Not implemented: message re-try after recoverable error")
		return "", false
	}

	// On success, return a string in the form of 'user_id:msg_id', and a bool indicating success
	return fmt.Sprintf("%s:%d", user.Id, sent.ID), true
}

// TelegramSender is a daemon-like function that listens to the notification
// and priority queues for incoming messages and notifications.
func Sender(tg *Bot) {
	const priorityQueueClearInterval = 10
	var processStartTime time.Time

	for {
		// Check notification queue
		if len(tg.Queue.Sendables) != 0 {
			// Lock queue while processing
			tg.Queue.Mutex.Lock()

			// Flip switch to indicate that we are sending notifications
			tg.Spam.NotificationSendUnderway = true

			for hash, sendable := range tg.Queue.Sendables {
				// Processing time
				processStartTime = time.Now()

				log.Info().Msgf("Processing sendable with hash=%s, type=%s", hash, sendable.Type)

				// Keep track of sent IDs
				sentIds := []string{}

				if sendable.Size >= 512 {
					log.Warn().Msgf("Sendable is %s bytes long, taking %d tokens per send", sendable.Size, sendable.Tokens)
				} else {
					if sendable.Size != 0 {
						log.Debug().Msgf("Sendable is %d bytes long, taking 1 token per send", sendable.Size)
					}
				}

				// Loop over users this sendable is meant for
				for i, chat := range sendable.Recipients {
					// Rate-limiter: check if we have tokens to proceed
					tg.Spam.GlobalLimiter(sendable.Tokens)

					// Run a light user-limiter: max tokens is 2
					tg.Spam.UserLimiter(chat, tg.Stats, 1)

					// Switch-case the sendable's type
					switch sendable.Type {
					case "notification":
						log.Debug().Msgf("Sending notification to user=%s", chat.Id)
						sentIdPair, success := SendNotification(tg, sendable, chat)

						if success {
							sentIds = append(sentIds, sentIdPair)
							chat.Stats.ReceivedNotifications++
						}
					case "delete":
						DeleteNotificationMessage(tg, sendable, chat)
					}

					/* Periodically, during long sends, check if the TelegramBot.PriorityQueued is set.
					This flag is enabled if there is one, or more, enqueued high-priority messages
					in the high-priority queue. Vary the priorityQueueClearInterval variable to tune
					how often to check for pending messages.

					The justification for this is the fact that the main queue's mutex is locked when
					the sending process is started, and could be locked for minutes. This alleviates
					the issue of messages sitting in the queue for ages, sacrificing the send time of
					mass-notifications and mass-removals for timely responses to commands. */
					if (tg.Queue.HighPriority.HasItemsInQueue) && (i%priorityQueueClearInterval == 0) {
						log.Debug().Msg("High-priority messages in queue during long send")
						clearPriorityQueue(tg)
					}
				}

				// Log time spent processing
				timeSpent := durafmt.Parse(time.Since(processStartTime)).LimitFirstN(1)

				// All done: do post-processing
				switch sendable.Type {
				case "notification":
					// Send done; log
					log.Info().Msgf("Sent %d notification(s) for sendable=%s in %s", len(sentIds), hash, timeSpent)

					// Update statistics, save to disk
					tg.Stats.Notifications += len(sentIds)
					tg.Db.SaveStatsToDisk(tg.Stats)

					// Save stats for all users in recipients (so we can track received notification count)
					tg.Db.SaveUserBatch(sendable.Recipients)

					// Load launch from cache so we can save the IDs
					launch, err := tg.Cache.FindLaunchById(sendable.LaunchId)

					if err != nil {
						log.Error().Err(err).Msgf("Unable to find launch while saving sent message IDs")
					}

					// If launch has previously sent notifications, delete them
					if launch.SentNotificationIds != "" {
						log.Info().Msgf("Launch has previously sent notifications, removing...")

						// Load IDs of previously sent notifications
						previouslySentIds := launch.LoadSentNotificationIdMap()

						// Create a sendable for removing mass-notifications
						deletionSendable := sendables.SendableForMessageRemoval(sendable, previouslySentIds)

						// Load recipients, so we can ignore chats that have muted this launch
						deletionSendable.Recipients = sendable.Recipients

						// Enqueue the sendable for removing the old notifications
						go tg.Queue.Enqueue(deletionSendable, false)
					}

					// Save the IDs of the sent notifications
					launch.SaveSentNotificationIds(sentIds, tg.Db)

				case "delete":
					log.Info().Msgf("Processed %d message removals in %s", len(sendable.MessageIDs), timeSpent)
				}

				// Delete sendable from the queue
				delete(tg.Queue.Sendables, hash)
			}

			// Unlock mutex after each sendable
			tg.Spam.NotificationSendUnderway = false
			tg.Queue.Mutex.Unlock()
		}

		// Check if priority queue is populated (and skip sleeping if one entry)
		if tg.Queue.HighPriority.HasItemsInQueue {
			clearPriorityQueue(tg)
		}

		// Clear queue every 50 ms
		time.Sleep(time.Duration(time.Millisecond * 50))
	}
}
