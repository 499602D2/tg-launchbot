package bots

import (
	"launchbot/notifications"
	"launchbot/users"
	"time"

	"github.com/rs/zerolog/log"
)

/* TelegramMessageSender listens to the message queue, clearing it
periodically. Runs separate from NotificationSender. */
func TelegramMessageSender() {

}

/* Notification sender is spawned manually whenever a notification is enqueued.
Unlike TelegramMessageSender, it does not act like a daemon that listens to
pending messages. */
func TelegramNotificationSender(notif *notifications.Notification, tg *TelegramBot) {
	/* TODO:
	- use exponential back-off: https://en.wikipedia.org/wiki/Exponential_backoff

	- lock notificationSender in session until sent (disallow overlapping sends)

	- use a generic sendable object instead of a notification
		- send e.g. delete requests fast
		- how ratelimited?
		- user Go-generics
			- add a type field to the sendable objects...?
	*/

	// Map users to sent notifications' ids
	var sentIds map[users.User]int

	for i, user := range *notif.Recipients["tg"].Users {
		// Send message
		sent, err := tg.Bot.Send(
			tb.ChatID(int64(notif.Message.Recipient.Id)),
			notif.Message.TextContent,
			&notif.Message.TelegramSendOpts,
		)

		if err != nil {
			handleSendError(notif, err)
		} else {
			// Successfully sent; store sent notification's ID for later use
			sentIds[user] = sent.ID
		}

		// Sleep long enough to stay within API limits: convert messagesPerSecond to ms
		if i < notif.Recipients["tg"].UserCount-1 {
			time.Sleep(time.Millisecond * time.Duration(1.0/notif.RateLimit*1000.0))
		}
	}

	// TODO: all sent, save the IDs of sent notifications
	// db.SaveSentNotificationIds()
	log.Warn().Msg("Not implemented: sent notification IDs will not be saved!")
}
