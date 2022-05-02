package bots

import (
	"fmt"
	"launchbot/db"
	"launchbot/sendables"
	"launchbot/users"
	"net/http"
	"strconv"
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
	bot.Handle("/next", tg.nextHandler)
	bot.Handle("/test", tg.fauxNotifHandler)
	bot.Handle(tb.OnCallback, tg.callbackHandler)

	// Assign
	tg.Bot = bot
}

func (tg *TelegramBot) pingHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Sender.ID), "tg")

	if !PreHandler(tg, user, message.Unixtime) {
		return nil
	}

	// Create the sendable
	sendable := sendables.TextOnlySendable("pong", user)

	// Add to send queue
	go tg.Queue.Enqueue(sendable, tg, true)

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) startHandler(c tb.Context) error {
	message := c.Message()
	user := *tg.Cache.FindUser(fmt.Sprint(message.Sender.ID), "tg")

	if !PreHandler(tg, &user, message.Unixtime) {
		return nil
	}

	// Create the sendable
	sendable := sendables.TextOnlySendable("pong", &user)

	// Add to send queue
	go tg.Queue.Enqueue(sendable, tg, true)

	// Check if the chat is actually new, or just calling /start again
	//if !stats.ChatExists(&message.Sender.ID, session.Config) {
	//	log.Println("🌟", message.Sender.ID, "bot added to new chat!")
	//}

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) scheduleHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Sender.ID), "tg")

	if !PreHandler(tg, user, message.Unixtime) {
		return nil
	}

	// Get text for the message
	scheduleMsg := tg.Cache.ScheduleMessage(user, false)

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

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) nextHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Sender.ID), "tg")

	if !PreHandler(tg, user, message.Unixtime) {
		return nil
	}

	// Get text for the message
	textContent, _ := tg.Cache.LaunchListMessage(user, 0, false)

	// Refresh button (schedule/refresh/vehicles)
	refreshBtn := tb.InlineButton{
		Text: "🔄 Refresh",
		Data: "nxt/r/0",
	}

	// Mode toggle button (schedule/mode/missions)
	nextBtn := tb.InlineButton{
		Text: "➡️ Next",
		Data: "nxt/n/1/+",
	}

	// Construct the keyboard
	kb := [][]tb.InlineButton{{nextBtn}, {refreshBtn}}

	// Construct message
	msg := sendables.Message{
		TextContent: &textContent,
		AddUserTime: true,
		RefTime:     tg.Cache.Launches[0].NETUnix,
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

	// TODO Save stats
	return nil
}

// Test notification sends
func (tg *TelegramBot) fauxNotifHandler(c tb.Context) error {
	// Admin-only function
	if c.Message().Sender.ID != tg.Owner {
		log.Error().Msgf("/test called by non-admin (%d)", c.Message().Sender.ID)
		return nil
	}

	// Load user from cache
	user := tg.Cache.FindUser(fmt.Sprint(c.Message().Sender.ID), "tg")

	// Create message, get notification type
	testId := c.Data()

	if len(testId) == 0 {
		sendable := sendables.TextOnlySendable("No launch ID entered", user)
		go tg.Queue.Enqueue(sendable, tg, true)
		return nil
	}

	launch, err := tg.Cache.FindLaunchById(testId)

	if err != nil {
		log.Error().Err(err).Msgf("Could not find launch by id=%s", testId)
		return nil
	}

	notifType := "1h"
	text := launch.NotificationMessage(notifType, false)

	muteBtn := tb.InlineButton{
		Text: "🔇 Mute launch",
		Data: fmt.Sprintf("mute/%s", launch.Id),
	}

	expandBtn := tb.InlineButton{
		Text: "ℹ️ Expand description",
		Data: fmt.Sprintf("exp/%s/%s", launch.Id, notifType),
	}

	// Construct the keeb
	kb := [][]tb.InlineButton{
		{muteBtn}, {expandBtn},
	}

	// Message
	msg := sendables.Message{
		TextContent: &text,
		AddUserTime: true,
		RefTime:     launch.NETUnix,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	recipients := users.SingleUserList(user, true, "tg")

	// Create sendable
	sendable := sendables.Sendable{
		Type: "notification", Message: &msg, Recipients: recipients,
		RateLimit: 20,
	}

	// Add to send queue
	go tg.Queue.Enqueue(&sendable, tg, true)

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) callbackHandler(c tb.Context) error {
	// Pointer to received callback
	cb := c.Callback()

	// User
	user := tg.Cache.FindUser(fmt.Sprint(cb.Sender.ID), "tg")

	// Enforce rate-limits
	if !PreHandler(tg, user, time.Now().Unix()) {
		return nil
	}

	// Split data field
	callbackData := strings.Split(cb.Data, "/")
	primaryRequest := callbackData[0]

	// Callback response
	var cbRespStr string

	// Toggle to show a persistent alert for errors
	showAlert := false

	// TODO switch-case over cmd (e.g. sch, nxt) to reduce code
	switch primaryRequest {
	case "sch":
		// Map for input validity check
		validInputs := map[string]bool{
			"v": true, "m": true,
		}

		// Check input length
		if len(callbackData) < 3 {
			log.Error().Msgf("Too short callback data in /schedule: %s", cb.Data)
			return nil
		}

		// Check input is valid
		_, ok := validInputs[callbackData[2]]

		if !ok {
			log.Warn().Msgf("Received invalid data in schedule callback handler: %s", cb.Data)
			return nil
		}

		// Get new text for the refresh (v for vehicles, m for missions)
		newText := tg.Cache.ScheduleMessage(user, callbackData[2] == "m")

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

		// Switch-case the callback response
		switch callbackData[1] {
		case "r":
			cbRespStr = "🔄 Schedule refreshed"
		case "m":
			cbRespStr = "🔄 Schedule loaded"
		}

		// Edit message
		_, err := tg.Bot.Edit(cb.Message, newText, &sendOptions)

		if err != nil {
			if !handleTelegramError(err, tg) {
				return nil
			}
		}
	case "nxt":
		// Get new text for the refresh
		idx, err := strconv.Atoi(callbackData[2])
		if err != nil {
			log.Error().Err(err).Msgf("Unable to convert nxt/r cbdata to int: %s", callbackData[2])
		}

		newText, keyboard := tg.Cache.LaunchListMessage(user, idx, true)

		// Send options: reuse keyboard
		sendOptions := tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: keyboard,
		}

		// Edit message
		_, err = tg.Bot.Edit(cb.Message, newText, &sendOptions)

		if err != nil {
			// If not recoverable, return
			if !handleTelegramError(err, tg) {
				return nil
			}
		}

		// Create callback response text
		switch callbackData[1] {
		case "r":
			cbRespStr = "🔄 Data refreshed"
		case "n":
			// Create callback response text
			switch callbackData[3] {
			case "+":
				cbRespStr = "Next launch ➡️"
			case "-":
				cbRespStr = "⬅️ Previous launch"
			default:
				log.Error().Msgf("Undefined behavior for callbackData in nxt/n (cbd[3]=%s)", callbackData[3])
				cbRespStr = "⚠️ Please do not send arbitrary data to the bot"
				showAlert = true
			}
		}
	case "exp": // Notification message content expansion
		// Verify input is valid
		if len(callbackData) < 3 {
			log.Error().Msgf("Invalid callback data length in /exp: %s", cb.Data)
			cbRespStr = "⚠️ Please do not send arbitrary data to the bot"
			showAlert = true
			break
		}

		// Extract ID and notification type
		launchId := callbackData[1]
		notifType := callbackData[2]

		// Find launch by ID (it may not exist in the cache anymore)
		launch, err := tg.Cache.FindLaunchById(launchId)

		if err != nil {
			cbRespStr = fmt.Sprintf("⚠️ %s", err.Error())
			showAlert = true
			break
		}

		// Get text for this launch
		newText := launch.NotificationMessage(notifType, true)
		newText = *sendables.SetTime(newText, user, launch.NETUnix, true)

		// Add mute button
		muteBtn := tb.InlineButton{
			Text: "🔇 Mute launch",
			Data: fmt.Sprintf("mute/%s", launch.Id),
		}

		// Construct the keyboard and send-options
		kb := [][]tb.InlineButton{{muteBtn}}
		sendOptions := tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		}

		// Edit message
		_, err = tg.Bot.Edit(cb.Message, newText, &sendOptions)

		if err != nil {
			// If not recoverable, return
			if !handleTelegramError(err, tg) {
				return nil
			}
		}

	default:
		// Handle invalid callback data
		log.Warn().Msgf("Invalid callback data: %s", cb.Data)
		return nil
	}

	// Create callback response
	cbResp := tb.CallbackResponse{
		CallbackID: cb.ID,
		Text:       cbRespStr,
		ShowAlert:  showAlert,
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
