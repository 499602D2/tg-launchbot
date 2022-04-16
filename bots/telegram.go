package bots

import (
	"launchbot/templates"
	"launchbot/users"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

type TelegramBot struct {
	Bot          *tb.Bot
	Queue        *Queue
	HighPriority *HighPriorityQueue
	Spam         *AntiSpam
	Owner        int64
}

type HighPriorityQueue struct {
	HasItemsInQueue bool
	Queue           []*Sendable
	Mutex           sync.Mutex
}

// Simple method to initialize the TelegramBot object
func (tg *TelegramBot) Initialize(token string) {
	// Create primary Telegram queue
	tg.Queue = &Queue{
		MessagesPerSecond: 4,
		Sendables:         make(map[string]*Sendable),
	}

	// Create the high-priority queue
	tg.HighPriority = &HighPriorityQueue{HasItemsInQueue: false}

	// Create the tb.Bot object
	bot, err := tb.NewBot(tb.Settings{
		Token:  token,
		Poller: &tb.LongPoller{Timeout: time.Second * 60},
		Client: &http.Client{Timeout: time.Second * 60},
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Error creating Telegram bot")
	}

	// Set-up command handlers
	// TODO add middle-ware for spam
	bot.Handle("/ping", tg.pingHandler)
	bot.Handle("/start", tg.startHandler)
	bot.Handle("/schedule", tg.scheduleHandler)

	// Assign
	tg.Bot = bot
}

func (tg *TelegramBot) pingHandler(c tb.Context) error {
	message := c.Message()
	if !CommandPreHandler(tg.Spam, &users.User{Platform: "tg", Id: message.Sender.ID}, message.Unixtime) {
		return nil
	}

	// Construct message
	text := "pong"
	msg := Message{
		TextContent: &text,
		SendOptions: tb.SendOptions{ParseMode: "Markdown"},
	}

	// Wrap into a sendable
	sendable := Sendable{
		Priority: int8(3), Type: "command", RateLimit: 5.0,
		Message:    &msg,
		Recipients: &users.UserList{Platform: "tg", Users: []*users.User{}},
	}

	// Add recipient to the user-list
	user := users.User{Platform: "tg", Id: message.Sender.ID}
	sendable.Recipients.Add(user, false)

	// Add to send queue
	go tg.Queue.Enqueue(&sendable, tg, true)

	return nil
}

func (tg *TelegramBot) startHandler(c tb.Context) error {
	message := c.Message()
	if !CommandPreHandler(tg.Spam, &users.User{Platform: "tg", Id: message.Sender.ID}, message.Unixtime) {
		return nil
	}

	// Construct message
	msg := Message{
		TextContent: templates.HelpMessage(),
		SendOptions: tb.SendOptions{ParseMode: "Markdown"},
	}

	// Wrap into a sendable
	sendable := Sendable{
		Priority: int8(3), Type: "command", RateLimit: 5.0,
		Message:    &msg,
		Recipients: &users.UserList{Platform: "tg", Users: []*users.User{}},
	}

	// Add recipient to the user-list
	user := users.User{Platform: "tg", Id: message.Sender.ID}
	sendable.Recipients.Add(user, false)

	// Add to send queue
	go tg.Queue.Enqueue(&sendable, tg, true)

	// Check if the chat is actually new, or just calling /start again
	//if !stats.ChatExists(&message.Sender.ID, session.Config) {
	//	log.Println("🌟", message.Sender.ID, "bot added to new chat!")
	//}

	return nil
}

func (tg *TelegramBot) scheduleHandler(c tb.Context) error { return nil }
