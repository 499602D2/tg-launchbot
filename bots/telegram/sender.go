package telegram

import (
	"errors"
	"fmt"
	"launchbot/sendables"
	"launchbot/users"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/hako/durafmt"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

/*
TODO

Modularize:
	- take an interface that implements e.g. sending functions and a queue
*/

// A simple notification job, with a sendable and a single recipient
type MessageJob struct {
	Sendable  *sendables.Sendable
	Recipient *users.User
	Results   chan string
	Id        string
}

// Enqueue a message into the appropriate queue
func (tg *Bot) Enqueue(sendable *sendables.Sendable, isCommand bool) {
	if isCommand {
		tg.CommandQueue <- sendable
	} else {
		tg.NotificationQueue <- sendable
	}
}

// Post-processing after a notification has been successfully sent
func (tg *Bot) NotificationPostProcessing(sendable *sendables.Sendable, sentIds []string) {
	// Add a deferred function that runs if we panic
	defer tg.gracefulPanic(sendable)

	// Update statistics, save to disk
	tg.Stats.Notifications += len(sentIds)
	tg.Db.SaveStatsToDisk(tg.Stats)

	// Load launch from cache so we can save the IDs
	launch, err := tg.Cache.FindLaunchById(sendable.LaunchId)

	if err != nil {
		log.Error().Err(err).Msgf("Unable to find launch while saving sent message IDs")
	}

	// Persist old notification IDs, if the user is not a current recipient
	filteredNotificationIds := sentIds

	// Users and ID-pairs for removal
	removalRecipients := []*users.User{}
	removalIdPairs := map[string]string{}

	// If launch has previously sent notifications, delete them
	if launch.SentNotificationIds != "" {
		log.Info().Msgf("Launch has previously sent notifications, removing...")

		// Load IDs of previously sent notifications
		previouslySentIds := launch.LoadSentNotificationIdMap()

		/* Get notification IDs that will not be deleted, i.e. all users who did
		not receive a notification now. Effectively, we just add certain IDs to
		the sentIds map.

		The previouslySentIds map effectively has "extra" recipients, which are not
		found in the current recipient list. */
		var userFound bool
		for userId, msgId := range previouslySentIds {
			userFound = false
			for _, recipientUser := range sendable.Recipients {
				if userId == recipientUser.Id {
					/* User is a current recipient and has a previously received
					notification, add to list of removals */
					removalRecipients = append(removalRecipients, recipientUser)
					removalIdPairs[userId] = msgId

					userFound = true
					continue
				}
			}

			if !userFound {
				// User not found in recipients: persist the id-pair
				log.Debug().Msgf("Persisting user=%s in sent notification ids (message=%s)", userId, msgId)
				filteredNotificationIds = append(filteredNotificationIds, fmt.Sprintf("%s:%s", userId, msgId))
			}
		}

		// Create a sendable for removing mass-notifications
		deletionSendable := sendables.SendableForMessageRemoval(sendable, removalIdPairs)

		// Set recipients, as in users that will have a previous notification removed
		deletionSendable.Recipients = removalRecipients

		// Enqueue the sendable for removing the old notifications
		tg.Enqueue(deletionSendable, false)

		// Sleep for a while so the message gets added to the queue
		time.Sleep(250 * time.Millisecond)
	}

	// Save the IDs of the sent notifications
	log.Debug().Msg("Saving sent notification IDs")
	launch.SaveSentNotificationIds(filteredNotificationIds, tg.Db)

	log.Debug().Msg("Notification post-processing completed")
	log.Debug().Msgf("WaitGroup done (NotificationPostProcessing), sendable.Type=%s", sendable.Type)
	tg.Quit.WaitGroup.Done()
}

// Process a notification or old notification removal. This path is not used
// for command messages.
func (tg *Bot) ProcessSendable(sendable *sendables.Sendable, workPool chan MessageJob) {
	// Track average processing time for notifications and message deletions
	processStartTime := time.Now()

	// Create the results channel
	results := make(chan string, len(sendable.Recipients))

	if sendable.Type == sendables.Notification {
		// Flip switch to indicate that we are sending notifications
		tg.Spam.NotificationSendUnderway = true

		// Calculate the size for this sendable
		sendable.Size = sendable.PerceivedByteSize()

		// Set token count for notifications
		if sendable.Size >= 512 {
			sendable.Tokens = 6
			log.Warn().Msgf("Sendable is %d bytes long, taking %d tokens per send", sendable.Size, sendable.Tokens)
		} else {
			sendable.Tokens = 1
			log.Debug().Msgf("Sendable is %d bytes long, taking 1 token per send", sendable.Size)
		}
	}

	// Loop over all the recipients of this sendable
	for i, chat := range sendable.Recipients {
		// Add job to work-pool (blocks if queue has more than $queueLength messages)
		workPool <- MessageJob{
			Sendable:  sendable,
			Recipient: chat,
			Results:   results,
			Id:        fmt.Sprintf("%s-%d", sendable.Type, i),
		}

		/* Periodically, during long sends, check if there are commands in the queue.
		Vary the modulo to tune how often to check for pending messages. At 25 msg/s,
		a modulo of 20 will result in approximately one second of delay. */
		if i%20 == 0 {
			select {
			case prioritySendable, ok := <-tg.CommandQueue:
				if ok {
					log.Debug().Msgf("High-priority message in queue during notification send")
					for n, priorityRecipient := range prioritySendable.Recipients {
						workPool <- MessageJob{
							Sendable:  prioritySendable,
							Recipient: priorityRecipient,
							Id:        fmt.Sprintf("%s-cmd-%d", sendable.Type, n),
						}
					}
				}

			default:
				break
			}
		}
	}

	// If this was a deletion, we can return early
	if sendable.Type == sendables.Delete {
		// Wait for all workers to finish before calculating processing time
		log.Debug().Msgf("Waiting for all deletion processes to finish...")
		for i := 0; i < len(sendable.Recipients); i++ {
			<-results
		}

		log.Debug().Msgf("Deletions done!")

		// Close results channel
		close(results)

		timeSpent := time.Since(processStartTime)

		log.Info().Msgf("Processed %d message removals in %s",
			len(sendable.MessageIDs), durafmt.Parse(timeSpent).LimitFirstN(2))

		log.Info().Msgf("Average deletion-rate %.1f msg/sec",
			float64(len(sendable.MessageIDs))/timeSpent.Seconds())

		log.Info().Msgf("Returning from ProcessSendable...")
		return
	}

	// Notification sending done: mark as finished
	tg.Spam.NotificationSendUnderway = false

	// Gather sent notification IDs
	sentIds := []string{}
	for i := 0; i < len(sendable.Recipients); i++ {
		idPair := <-results

		if idPair != "" {
			sentIds = append(sentIds, idPair)
		}
	}

	// Close results channel
	close(results)

	// Log how long processing took
	timeSpent := time.Since(processStartTime)

	// Notifications have been sent: log
	log.Info().Msgf("Sent %d notification(s) for sendable=%s:%s in %s",
		len(sentIds), sendable.NotificationType, sendable.LaunchId,
		durafmt.Parse(timeSpent).LimitFirstN(2))

	log.Info().Msgf("Average send-rate %.1f msg/sec",
		float64(len(sentIds))/timeSpent.Seconds())

	// Post-process the notification send, in a go-routine to avoid blocking
	log.Debug().Msgf("Entering NotificationPostProcessing, sendable.Type==%s", sendable.Type)
	go tg.NotificationPostProcessing(sendable, sentIds)
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
	strMessageId, ok := sendable.MessageIDs[user.Id]

	if !ok {
		// If chat has not received a previous notification, do nothing
		return
	}

	// Convert chat ID to integer
	chatId, err := strconv.ParseInt(user.Id, 10, 64)

	if err != nil {
		log.Error().Err(err).Msgf("Forming chat ID failed while removing sent messages")
		return
	}

	// Convert message ID to an integer
	messageId, err := strconv.Atoi(strMessageId)

	if err != nil {
		log.Error().Err(err).Msgf("Forming message ID failed while removing sent messages")
		return
	}

	// Build tb.Message, and delete it
	messageToBeDeleted := &tb.Message{ID: messageId, Chat: &tb.Chat{ID: int64(chatId)}}
	err = tg.Bot.Delete(messageToBeDeleted)

	if err != nil {
		tg.handleError(nil, messageToBeDeleted, err, int64(chatId))
		log.Error().Err(err).Msgf("Deleting message %s:%s failed", user.Id, strMessageId)
	}
}

// Send a notification
func (tg *Bot) SendNotification(sendable *sendables.Sendable, user *users.User, retryCount int) (string, bool) {
	// Convert id to an integer
	id, _ := strconv.ParseInt(user.Id, 10, 64)

	// Set monospacing based on notification type
	monospaced := false

	if sendable.NotificationType == "postpone" {
		monospaced = true
	}

	var text string
	if sendable.Message.AddUserTime {
		text = sendables.SetTime(sendable.Message.TextContent, user, sendable.Message.RefTime, true, monospaced, false)
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
		log.Warn().Msgf("Recoverable error in sender (re-try count = %d)", retryCount)
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
func (tg *Bot) NotificationWorker(id int, jobChannel chan MessageJob) {
	// Loop over the channel as long as it's open
	for job := range jobChannel {
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
				job.Recipient.Stats.ReceivedNotifications++
			} else {
				log.Warn().Msgf("[Worker=%d] Sending notification to chat=%s failed [%s]",
					id, job.Recipient.Id, job.Id)
			}

			job.Results <- idPair

		case sendables.Delete:
			tg.DeleteNotificationMessage(job.Sendable, job.Recipient)
			job.Results <- ""

		case sendables.Command:
			tg.SendCommand(job.Sendable.Message, job.Recipient)
			tg.Quit.WaitGroup.Done()

		default:
			log.Warn().Msgf("Invalide sendable type in NotificationWorker: %s", job.Sendable.Type)
		}

		// log.Debug().Msgf("[Worker=%d] Processed job (%s)", id, job.Id)
	}

	tg.Quit.Channel <- id
}

// Gracefully shut the message channels down
func (tg *Bot) Close(workPool chan MessageJob, workerCount int) {
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

func (tg *Bot) gracefulPanic(sendable *sendables.Sendable) {
	if err := recover(); err != nil {
		log.Error().Msgf("Ran into an exception in ThreadedSender, err: %+v", err)

		if sendable != nil {
			log.Error().Msgf("Sendable associated with this error: %+v", sendable)
		}

		// Attempt logging the stack
		log.Error().Msgf("%s", string(debug.Stack()[:]))

		// Attempt a graceful exit
		log.Warn().Msg("Sending SIGINT...")
		err := syscall.Kill(syscall.Getpid(), syscall.SIGINT)

		if err != nil {
			log.Error().Err(err).Msgf("Error sending SIGINT signal: exiting...")
			os.Exit(0)
		}

		// Sleep so the main function has time to capture the signal
		time.Sleep(time.Second)

		// Read the signal sent by the main function, set quit process as finalized
		<-tg.Quit.Channel
		tg.Quit.Finalized = true

		// Signal went through: lock the mutex, sleep for a while
		tg.Quit.Mutex.Lock()
		time.Sleep(time.Second)

		// Unlock the mutex so the main function can acquire a lock and receive the final signal
		tg.Quit.Mutex.Unlock()
		tg.Quit.Channel <- -1
	}
}

// ThreadedSender listens on a channel for incoming sendables
func (tg *Bot) ThreadedSender() {
	// Add a deferred function that runs if we panic
	defer tg.gracefulPanic(nil)

	// Queue for mass-sends, e.g. notifications and deletions
	tg.NotificationQueue = make(chan *sendables.Sendable)

	// Command queue contains singular sendables
	tg.CommandQueue = make(chan *sendables.Sendable)

	// Channel listening for a quit signal
	tg.Quit.Channel = make(chan int)
	tg.Quit.WaitGroup = &sync.WaitGroup{}

	/* Maximum worker-count during sending. Depending on what kind of day
	Telegram's API is having, one worker will typically do anywhere from
	5–10 sent messages per second. Thus, four workers should be adequate. */
	const workerCount = 4

	/* The pool the dequeued, processed sendables are thrown into. The buffered
	size ensures that high-priority messages from the command queue can be regularly
	dequeued, without having to wait for 1000+ notifications to finish sending first. */
	workPool := make(chan MessageJob, workerCount*2)

	// Spawn the workers
	for workerId := 1; workerId <= workerCount; workerId++ {
		go tg.NotificationWorker(workerId, workPool)
	}

	for {
		select {
		case sendable, ok := <-tg.NotificationQueue:
			if ok {
				// In the case of notifications, pre-process them first
				switch sendable.Type {
				case sendables.Delete:
					tg.Quit.WaitGroup.Add(1)
					log.Debug().Msg("Case sendables.Delete: adding 1 to waitgroup...")
				case sendables.Notification:
					log.Debug().Msg("Case sendables.Notification: adding 2 to waitgroup...")
					tg.Quit.WaitGroup.Add(2)
				default:
					log.Warn().Msgf("Unknown sendable type in ThreadedSender: %s", sendable.Type)
				}

				tg.ProcessSendable(sendable, workPool)
				log.Debug().Msg("Exited from ProcessSendable()")
				tg.Quit.WaitGroup.Done()
				log.Debug().Msg("WaitGroup done (ThreadedSender)")
			}

		case sendable, ok := <-tg.CommandQueue:
			if ok {
				// For high-priority messages, we don't need pre-processing
				tg.Quit.WaitGroup.Add(1)
				workPool <- MessageJob{
					Sendable:  sendable,
					Recipient: sendable.Recipients[0],
					Id:        "command",
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

		time.Sleep(50 * time.Millisecond)
	}

	// Send final quit-signal and unlock mutex
	tg.Quit.Mutex.Unlock()
	tg.Quit.Channel <- -1
}
