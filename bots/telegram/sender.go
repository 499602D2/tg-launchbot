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

/* TODO
+ Modularize:
	- take an interface that implements e.g. sending functions and a queue
*/

// A simple notification job, with a sendable and a single recipient
type NotificationJob struct {
	Sendable  *sendables.Sendable
	Recipient *users.User
}

// Enqueue a message into the appropriate queue
func (tg *Bot) Enqueue(sendable *sendables.Sendable, isCommand bool) {
	if isCommand {
		tg.CommandQueue <- sendable
	} else {
		tg.NotificationQueue <- sendable
	}
}

// CommandSender sends high-priority command replies.
func (tg *Bot) SendCommand(message *sendables.Message, chat *users.User) bool {
	// Extract text
	text := message.TextContent

	if message.AddUserTime {
		// If message needs to have its time set properly, do it now
		text = sendables.SetTime(text, chat, message.RefTime, true, true, false)
	}

	id, _ := strconv.ParseInt(chat.Id, 10, 64)

	// FUTURE use sendable.Send()
	sent, err := tg.Bot.Send(tb.ChatID(id), text, &message.SendOptions)

	if err != nil {
		if !tg.handleError(nil, sent, err, int64(id)) {
			// If error is unrecoverable, continue the loop
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

// Delete a Telegram message with a chat ID and a message ID
func (tg *Bot) DeleteNotificationMessage(sendable *sendables.Sendable, user *users.User) {
	// Load ID pair
	msgId, ok := sendable.MessageIDs[user.Id]

	if !ok {
		log.Warn().Msgf("Unable to find message ID during deletion for user=%s", user.Id)
		return
	}

	// Convert chat ID to integer
	chatId, err := strconv.ParseInt(user.Id, 10, 64)

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
func (tg *Bot) SendNotification(sendable *sendables.Sendable, user *users.User, retryCount int) (string, bool) {
	// Convert id to an integer
	id, _ := strconv.ParseInt(user.Id, 10, 64)

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

		// If a unrecoverable error, continue
		if !tg.handleError(nil, sent, err, int64(id)) {
			log.Warn().Msg("Unrecoverable error in sender, continuing loop")
			return "", false
		}

		// Error is recoverable: try sending again twice
		log.Warn().Msgf("Recoverable error in sender (re-try count = %d", retryCount)
		if retryCount < 3 {
			log.Debug().Msgf("Trying to send again...")
			return tg.SendNotification(sendable, user, retryCount+1)
		}

		return "", false
	}

	// On success, return a string in the form of 'user_id:msg_id', and a bool indicating success
	return fmt.Sprintf("%s:%d", user.Id, sent.ID), true
}

// NotificationWorker processes individual message delivery and removal jobs.
func (tg *Bot) NotificationWorker(id int, jobChannel <-chan NotificationJob, results chan<- string) {
	// Loop over the channel as long as it's open
	for job := range jobChannel {
		log.Debug().Msgf("[Worker=%d] Processing sendable...", id)
		if job.Sendable.Type != sendables.Command {
			/* If this is a notification or a message removal, take tokens. We can
			skip this for command replies, as the spam manager handles those. */
			tg.Spam.GlobalLimiter(job.Sendable.Tokens)
		}

		// Switch-case the type of the sendable
		switch job.Sendable.Type {
		case sendables.Notification:
			// Send notification, get sent ID
			idPair, success := tg.SendNotification(job.Sendable, job.Recipient, 0)

			if success {
				// On success, write the sent notification's ID to results channel
				results <- idPair
				job.Recipient.Stats.ReceivedNotifications++
			} else {
				results <- ""
				log.Warn().Msgf("Sending notification to chat=%s failed", job.Recipient.Id)
			}

		case sendables.Delete:
			tg.DeleteNotificationMessage(job.Sendable, job.Recipient)

		case sendables.Command:
			tg.SendCommand(job.Sendable.Message, job.Recipient)
			tg.Quit.WaitGroup.Done()
		}
	}

	tg.Quit.Channel <- id
}

// Post-processing after a notification has been successfully sent
func (tg *Bot) NotificationPostProcessing(sendable *sendables.Sendable, sentIds []string) {
	// Update statistics, save to disk
	tg.Stats.Notifications += len(sentIds)
	tg.Db.SaveStatsToDisk(tg.Stats)

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

		/* Re-use recipients. A user may not have received any notifications,
		but trying the removal for all recipients is safe as no-one on the list
		can have the launch muted. */
		deletionSendable.Recipients = sendable.Recipients

		// Enqueue the sendable for removing the old notifications
		tg.Enqueue(deletionSendable, false)
	}

	// Save the IDs of the sent notifications
	log.Debug().Msg("Saving sent notification IDs")
	launch.SaveSentNotificationIds(sentIds, tg.Db)

	log.Debug().Msg("Notification post-processing completed")
	tg.Quit.WaitGroup.Done()
}

// Process a notification or notification removal sendable
func (tg *Bot) ProcessSendable(sendable *sendables.Sendable, workPool chan<- NotificationJob, results <-chan string) {
	// Track average processing time for notifications and message deletions
	processStartTime := time.Now()

	// Pre-processing for notifications
	if sendable.Type == sendables.Notification {
		// Flip switch to indicate that we are sending notifications
		tg.Spam.NotificationSendUnderway = true

		// Set token count for notifications
		if sendable.Size >= 512 {
			log.Warn().Msgf("Sendable is %s bytes long, taking %d tokens per send", sendable.Size, sendable.Tokens)
		} else {
			if sendable.Size != 0 {
				log.Debug().Msgf("Sendable is %d bytes long, taking 1 token per send", sendable.Size)
			}
		}
	}

	// Loop over all the recipients of this sendable
	for i, chat := range sendable.Recipients {
		// Add job to work-pool (blocks if queue has more than $queueLength messages)
		workPool <- NotificationJob{Sendable: sendable, Recipient: chat}

		/* Periodically, during long sends, check if there are commands in the queue.
		Vary the modulo to tune how often to check for pending messages. At 25 msg/s,
		a modulo of 25 will result in approximately one second of delay. */
		if i%25 == 0 {
			select {
			case prioritySendable, ok := <-tg.CommandQueue:
				if ok {
					log.Debug().Msgf("High-priority message in queue during notification send")
					for _, priorityRecipient := range prioritySendable.Recipients {
						tg.Quit.WaitGroup.Add(1)
						workPool <- NotificationJob{Sendable: prioritySendable, Recipient: priorityRecipient}
					}
				}
			default:
				continue
			}
		}
	}

	// Log how long processing took
	timeSpent := time.Since(processStartTime)

	// If this was a deletion, we can return early
	switch sendable.Type {
	case sendables.Delete:
		log.Info().Msgf("Processed %d message removals in %s",
			len(sendable.MessageIDs), durafmt.Parse(timeSpent).LimitFirstN(2))

		log.Info().Msgf("Average deletion-rate %.1f msg/sec",
			float64(len(sendable.MessageIDs))/timeSpent.Seconds())

		tg.Quit.WaitGroup.Done()
		return
	}

	// Notification sending done
	tg.Spam.NotificationSendUnderway = false

	// Gather sent notification IDs
	sentIds := []string{}
	for i := 0; i < len(sendable.Recipients); i++ {
		idPair := <-results

		if idPair != "" {
			sentIds = append(sentIds, idPair)
		}
	}

	// Notifications have been sent: log
	log.Info().Msgf("Sent %d notification(s) for sendable=%s:%s in %s",
		len(sentIds), sendable.NotificationType, sendable.LaunchId,
		durafmt.Parse(timeSpent).LimitFirstN(2))

	log.Info().Msgf("Average send-rate %.1f msg/sec",
		float64(len(sentIds))/timeSpent.Seconds())

	// Post-process the notification send, in a go-routine to avoid blocking
	tg.Quit.WaitGroup.Add(1)
	go tg.NotificationPostProcessing(sendable, sentIds)

	// Done
	tg.Quit.WaitGroup.Done()
}

// Gracefully shut the message channels down
func (tg *Bot) Close(workPool chan<- NotificationJob, workerCount int) {
	// Wait for all workers to finish their jobs
	log.Debug().Msg("Waiting for workers to finish...")
	tg.Quit.WaitGroup.Wait()

	log.Debug().Msg("All workers finished")

	// Close channels
	close(tg.NotificationQueue)
	close(tg.CommandQueue)
	close(workPool)

	log.Debug().Msg("All channels closed")
}

// ThreadedSender listens on a channel for incoming sendables
func (tg *Bot) ThreadedSender() {
	// Create the job pool, where the workers get their jobs from
	tg.NotificationQueue = make(chan *sendables.Sendable)

	/* Jobs are de-queued from the priority queue into the main queue during
	notification sends. */
	tg.CommandQueue = make(chan *sendables.Sendable)

	// Channel listening for a quit signal
	tg.Quit.Channel = make(chan int)

	/* Maximum worker-count during sending. Depending on what kind of day
	Telegram's API is having, one worker will typically do anywhere from
	5–10 sent messages per second. Thus, four workers should be adequate. */
	const workerCount = 4

	/* Job pool the dequeued, processed sendables are thrown into. The buffered
	size ensures that high-priority messages are not immediately dequeued during
	long sends, alleviating possible spam issues. */
	queueLength := workerCount * 2
	workPool := make(chan NotificationJob, queueLength)

	// The result channel delivers message ID pairs of delivered messages
	results := make(chan string)

	// Spawn the workers
	for workerId := 1; workerId <= workerCount; workerId++ {
		go tg.NotificationWorker(workerId, workPool, results)
	}

	for {
		select {
		case sendable, ok := <-tg.NotificationQueue:
			if ok {
				// In the case of notifications, pre-process them first
				tg.Quit.WaitGroup.Add(1)
				tg.ProcessSendable(sendable, workPool, results)
			}

		case sendable, ok := <-tg.CommandQueue:
			if ok {
				// For high-priority messages, we don't need pre-processing
				tg.Quit.WaitGroup.Add(1)
				workPool <- NotificationJob{
					Sendable: sendable, Recipient: sendable.Recipients[0],
				}
			}

		case quit := <-tg.Quit.Channel:
			if !tg.Quit.Started {
				// Indicate that the sender shutdown has started
				tg.Quit.Started = true
				tg.Quit.Mutex.Lock()

				// In a go-routine, wait for workers to finish and close all channels
				go tg.Close(workPool, workerCount)
			} else {
				// If the quit has started, the message is a worker indicating closing
				log.Debug().Msgf("Received quit-signal from worker=%d", quit)
				tg.Quit.ExitedWorkers++

				if tg.Quit.ExitedWorkers == workerCount {
					// Once all workers have exited, flip the flag
					tg.Quit.Finalized = true
				}
			}
		}

		if tg.Quit.Finalized {
			log.Debug().Msg("Quit process finalized")
			break
		}

		time.Sleep(time.Millisecond * time.Duration(50))
	}

	// Send final quit-signal and unlock mutex
	tg.Quit.Mutex.Unlock()
	tg.Quit.Channel <- -1
}
