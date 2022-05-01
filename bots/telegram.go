package bots

import (
	"fmt"
	"launchbot/db"
	"launchbot/sendables"
	"launchbot/users"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

type TelegramBot struct {
	Bot          *tb.Bot
	Db           *db.Database
	Cache        *db.Cache
	Queue        *Queue
	HighPriority *HighPriorityQueue
	Spam         *AntiSpam
	Owner        int64
}

type HighPriorityQueue struct {
	HasItemsInQueue bool
	Queue           []*sendables.Sendable
	Mutex           sync.Mutex
}

// Simple method to initialize the TelegramBot object
func (tg *TelegramBot) Initialize(token string) {
	// Create primary Telegram queue
	tg.Queue = &Queue{
		MessagesPerSecond: 4,
		Sendables:         make(map[string]*sendables.Sendable),
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

	// Set-up command and callback handlers
	// TODO add middle-ware for spam
	bot.Handle("/ping", tg.pingHandler)
	bot.Handle("/start", tg.startHandler)
	bot.Handle("/schedule", tg.scheduleHandler)
	bot.Handle(tb.OnCallback, tg.callbackHandler)

	// Assign
	tg.Bot = bot
}

func (tg *TelegramBot) pingHandler(c tb.Context) error {
	message := c.Message()
	user := *tg.Cache.FindUser(fmt.Sprint(message.Sender.ID), "tg")

	if !PreHandler(tg, &user, message.Unixtime) {
		return nil
	}

	// Construct message
	text := "pong"
	msg := sendables.Message{
		TextContent: &text,
		SendOptions: tb.SendOptions{ParseMode: "Markdown"},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type: "command", RateLimit: 5.0,
		Message:    &msg,
		Recipients: users.SingleUserList(message.Sender.ID, false, "tg"),
	}

	// Add to send queue
	go tg.Queue.Enqueue(&sendable, tg, true)

	// Save stats
	// TODO regularly dump stats to disk (e.g. whenever user cache is cleaned)
	go tg.Db.SaveUser(&user)

	return nil
}

func (tg *TelegramBot) startHandler(c tb.Context) error {
	message := c.Message()
	user := *tg.Cache.FindUser(fmt.Sprint(message.Sender.ID), "tg")

	if !PreHandler(tg, &user, message.Unixtime) {
		return nil
	}

	txt := "pong"

	// Construct message
	msg := sendables.Message{
		TextContent: &txt,
		SendOptions: tb.SendOptions{ParseMode: "Markdown"},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type: "command", RateLimit: 5.0,
		Message:    &msg,
		Recipients: users.SingleUserList(message.Sender.ID, false, "tg"),
	}

	// Add to send queue
	go tg.Queue.Enqueue(&sendable, tg, true)

	// Check if the chat is actually new, or just calling /start again
	//if !stats.ChatExists(&message.Sender.ID, session.Config) {
	//	log.Println("🌟", message.Sender.ID, "bot added to new chat!")
	//}

	return nil
}

func (tg *TelegramBot) scheduleHandler(c tb.Context) error {
	message := c.Message()
	user := *tg.Cache.FindUser(fmt.Sprint(message.Sender.ID), "tg")

	if !PreHandler(tg, &user, message.Unixtime) {
		return nil
	}

	// Get text for the message
	scheduleMsg := tg.Cache.ScheduleMessage(&user, false)

	// Refresh button (schedule/refresh/vehicles)
	updateBtn := tb.InlineButton{
		Text: "🔄 Refresh",
		Data: "sch/r/v",
	}

	// Mode toggle button (schedule/mode/missions)
	modeBtn := tb.InlineButton{
		Text: "🛰️ Missions",
		Data: "sch/m/m",
	}

	// Construct the keyboard
	kb := [][]tb.InlineButton{{updateBtn, modeBtn}}

	// Construct message
	msg := sendables.Message{
		TextContent: &scheduleMsg,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type: "command", RateLimit: 5.0,
		Message:    &msg,
		Recipients: &users.UserList{},
	}

	sendable.Recipients.Add(user, false)

	// Add to send queue
	go tg.Queue.Enqueue(&sendable, tg, true)

	return nil
}

func (tg *TelegramBot) callbackHandler(c tb.Context) error {
	// Pointer to received callback
	cb := c.Callback()

	// User
	user := *tg.Cache.FindUser(fmt.Sprint(cb.Sender.ID), "tg")

	// Enforce rate-limits
	if !PreHandler(tg, &user, time.Now().Unix()) {
		return nil
	}

	// Split data field
	callbackData := strings.Split(cb.Data, "/")
	primaryRequest := fmt.Sprintf("%s/%s", callbackData[0], callbackData[1])

	// Callback response
	var cbRespStr string

	switch primaryRequest {
	case "sch/r":
		// Get new text for the refresh (v for vehicles, m for missions)
		newText := tg.Cache.ScheduleMessage(&user, callbackData[2] == "m")

		// Send options: reuse keyboard
		sendOptions := tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: cb.Message.ReplyMarkup,
		}

		// Edit message
		_, err := tg.Bot.Edit(cb.Message, newText, &sendOptions)

		if err != nil {
			// If not recoverable, return
			if !handleTelegramError(err, tg) {
				return nil
			}
		}

		// Create callback response text
		cbRespStr = "🔄 Schedule refreshed"
	case "sch/m":
		// Map for input validity check
		validInputs := map[string]bool{
			"v": true, "m": true,
		}

		// Check input is valid
		_, ok := validInputs[callbackData[2]]
		if !ok {
			log.Warn().Msgf("Received invalid data in sch/m callback handler: %s", callbackData[2])
		}

		// Get new text for mode change (v for vehicles, m for missions)
		newText := tg.Cache.ScheduleMessage(&user, callbackData[2] == "m")

		// Refresh button (schedule/refresh/vehicles)
		updateBtn := tb.InlineButton{
			Text: "🔄 Refresh",
			Data: fmt.Sprintf("sch/r/%s", callbackData[2]),
		}

		// Init the mode-switch button
		modeBtn := tb.InlineButton{}

		switch callbackData[2] {
		case "m":
			modeBtn.Text = "🚀 Vehicles"
			modeBtn.Data = "sch/m/v"
		case "v":
			modeBtn.Text = "🛰️ Missions"
			modeBtn.Data = "sch/m/m"
		}

		// Construct the keyboard
		kb := [][]tb.InlineButton{{updateBtn, modeBtn}}

		// Send options: new keyboard
		sendOptions := tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		}

		// Edit message
		_, err := tg.Bot.Edit(cb.Message, newText, &sendOptions)

		if err != nil {
			// If not recoverable, return
			if !handleTelegramError(err, tg) {
				return nil
			}
		}

		// Create callback response text
		cbRespStr = "🔄 Schedule loaded"
	default:
		// Handle invalid callback data
		log.Warn().Msgf("Invalid callback data: %s", cb.Data)
		return nil
	}

	// Create callback response
	cbResp := tb.CallbackResponse{
		CallbackID: cb.ID,
		Text:       cbRespStr,
		ShowAlert:  false,
	}

	// Respond to callback
	// TODO throw to queue as a sendable
	err := tg.Bot.Respond(cb, &cbResp)

	if err != nil {
		log.Error().Err(err).Msg("Error responding to callback")
		handleTelegramError(err, tg)
	}

	return nil
}
