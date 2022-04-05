package notifications

import (
	"launchbot/launch"
	"launchbot/users"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// A scheduled notification
type Notification struct {
	Launch     *launch.Launch
	SendTime   time.Time
	Recipients map[string]users.UserList // Map[dg, tg, email] -> userList
	Message    Message
	RateLimit  float32
}

// The message content of a notification
type Message struct {
	TextContent      *string
	Recipient        users.User
	TelegramSendOpts tb.SendOptions
}

type Queue struct {
}

func (notif *Notification) Send() {
	/*
		Simply add the Notification object to a sendQueue
		- Queue is imported into config.Session.Queue
		- Queue is of type notifications.Queue
		- ...?
	*/
	for _, userList := range notif.Recipients {
		// Loop over the users, distribute into appropriate send queues
		switch userList.Platform {
		case "tg":
			log.Warn().Msg("Telegram message sender not implemented!")
		case "dg":
			log.Warn().Msg("Discord message sender not implemented!")
		case "email":
			log.Warn().Msg("Email sender not implemented!")
		}
	}
}
