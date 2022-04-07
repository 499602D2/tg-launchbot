package bots

import (
	"launchbot/templates"
	"launchbot/users"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

type TelegramBot struct {
	Bot          *tb.Bot
	MessageQueue *Queue
	HighPriority HighPriorityQueue
}

type HighPriorityQueue struct {
	HasItemsInQueue bool
	Queue           []*Sendable
	Mutex           sync.Mutex
}

func (tgBot *TelegramBot) Initialize() {
	// Create primary Telegram message queue
	tgQueue := Queue{MessagesPerSecond: 4}
	tgMessages := make(map[string]*Sendable)
	tgQueue.Messages = tgMessages

	tgBot.Bot = nil
	tgBot.MessageQueue = &tgQueue

	tgBot.HighPriority = HighPriorityQueue{HasItemsInQueue: false}
}

func SetupTelegramBot(token string, aspam *AntiSpam, queue *Queue, tg *TelegramBot) *tb.Bot {
	var err error
	bot, err := tb.NewBot(tb.Settings{
		Token:  token,
		Poller: &tb.LongPoller{Timeout: 30 * time.Second},
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Error creating Telegram bot")
	}

	// Command handler for /start
	bot.Handle("/start", func(c tb.Context) error {
		// Anti-spam
		message := c.Message()
		if !CommandPreHandler(aspam, &users.User{Platform: "tg", Id: message.Sender.ID}, message.Unixtime) {
			return nil
		}

		// Construct message
		startMessage := templates.HelpMessage()
		msg := Message{
			TextContent: &startMessage,
			SendOptions: tb.SendOptions{ParseMode: "Markdown"},
		}

		// Wrap into a sendable
		userList := users.UserList{Platform: "tg", Users: []*users.User{}}
		sendable := Sendable{
			Priority: int8(3), Type: "command", RateLimit: 5.0,
			Message: &msg, Recipients: &userList,
		}

		// Add recipient to the user-list
		user := users.User{Platform: "tg", Id: message.Sender.ID}
		sendable.Recipients.Add(user, false)

		// Add to send queue
		queue.Enqueue(&sendable, tg, true)

		// Check if the chat is actually new, or just calling /start again
		//if !stats.ChatExists(&message.Sender.ID, session.Config) {
		//	log.Println("🌟", message.Sender.ID, "bot added to new chat!")
		//}

		return nil
	})

	// Return bot after setup
	return bot
}
