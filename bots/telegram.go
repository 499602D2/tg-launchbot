package bots

import (
	"fmt"
	"launchbot/templates"
	"launchbot/users"
	"sync"

	tb "gopkg.in/telebot.v3"
)

type TelegramBot struct {
	Bot          *tb.Bot
	MessageQueue Queue
}

/* A single Telegram message */
type TelegramMessage struct {
	TextContent      *string
	Recipient        users.User
	TelegramSendOpts tb.SendOptions
}

/* A queue of Telegram messages to be sent */
type Queue struct {
	MessagesPerSecond float32            // Messages-per-second limit
	Messages          *[]TelegramMessage // Queue of Telegrammessages to send
	Mutex             sync.Mutex         // Mutex to avoid concurrent writes
}

/* TODO TODO TODO
- implement a more generic TelegramMessage format for pushing Telegram methods,
	i.e. in the case where we remove thousands of notifications at once.
*/

/* Adds a message to the Telegram message queue */
func (queue *Queue) Enqueue(message *TelegramMessage) {
	queue.Mutex.Lock()
	*queue.Messages = append(*queue.Messages, *message)
	queue.Mutex.Unlock()
}

func SetupTelegramBot(bot *tb.Bot, aspam *AntiSpam, queue *Queue) {
	// Pull pointers from session for cleaner code
	//aspam := session.Spam

	// Command handler for /start
	bot.Handle("/start", func(c tb.Context) error {
		// Anti-spam
		message := c.Message()
		if !CommandPreHandler(aspam, &users.User{Platform: "tg", Id: message.Sender.ID}, message.Unixtime) {
			return nil
		}

		// Construct message
		startMessage := templates.HelpMessage()
		msg := TelegramMessage{
			TextContent:      &startMessage,
			Recipient:        users.User{Platform: "tg", Id: message.Sender.ID},
			TelegramSendOpts: tb.SendOptions{ParseMode: "Markdown"},
		}

		// Add to send queue
		queue.Enqueue(&msg)
		fmt.Printf("NOT IMPL: not adding message to queue! %d", msg.Recipient.Id)

		// Check if the chat is actually new, or just calling /start again
		//if !stats.ChatExists(&message.Sender.ID, session.Config) {
		//	log.Println("🌟", message.Sender.ID, "bot added to new chat!")
		//}

		return nil
	})
}
