package bots

import (
	"fmt"
	"launchbot/db"
	"launchbot/sendables"
	"launchbot/stats"
	"launchbot/users"
	"launchbot/utils"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bradfitz/latlong"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

type TelegramBot struct {
	Bot             *tb.Bot
	Db              *db.Database
	Cache           *db.Cache
	Queue           *Queue
	HighPriority    *HighPriorityQueue
	Spam            *AntiSpam
	Stats           *stats.Statistics
	TZSetupMessages map[int64]int64 // A map of msg_id:user_id time zone setup messages waiting for a reply
	Owner           int64
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

	// Set-up command handlers
	// TODO add middle-ware for spam
	bot.Handle("/ping", tg.pingHandler)
	bot.Handle("/start", tg.startHandler)
	bot.Handle("/settings", tg.settingsHandler)
	bot.Handle("/next", tg.nextHandler)
	bot.Handle("/schedule", tg.scheduleHandler)
	bot.Handle("/statistics", tg.statsHandler)

	// Handle callbacks
	bot.Handle(tb.OnCallback, tg.callbackHandler)

	// Handle incoming locations for time zone setup messages
	bot.Handle(tb.OnLocation, tg.locationReplyHandler)

	// Assign
	tg.Bot = bot
}

func (tg *TelegramBot) pingHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Chat.ID), "tg")

	if !PreHandler(tg, user, c) {
		return nil
	}

	// Create the sendable
	sendable := sendables.TextOnlySendable("pong", user)

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(sendable, tg, true)

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) startHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Chat.ID), "tg")

	if !PreHandler(tg, user, c) {
		return nil
	}

	// Create the sendable
	sendable := sendables.TextOnlySendable("pong", user)

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(sendable, tg, true)

	// Check if the chat is actually new, or just calling /start again
	//if !stats.ChatExists(&message.Chat.ID, session.Config) {
	//	log.Println("🌟", message.Chat.ID, "bot added to new chat!")
	//}

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) scheduleHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Chat.ID), "tg")

	if !PreHandler(tg, user, c) {
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

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) nextHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Chat.ID), "tg")

	if !PreHandler(tg, user, c) {
		return nil
	}

	// Get text for the message
	textContent, _ := tg.Cache.LaunchListMessage(user, 0, false)

	refreshBtn := tb.InlineButton{
		Text: "🔄 Refresh",
		Data: "nxt/r/0",
	}

	nextBtn := tb.InlineButton{
		Text: "Next launch ➡️",
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

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) statsHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Chat.ID), "tg")

	if !PreHandler(tg, user, c) {
		return nil
	}

	textContent := tg.Stats.String()

	// Construct the keyboard and send-options
	kb := [][]tb.InlineButton{{
		tb.InlineButton{
			Text: "🔄 Refresh data",
			Data: "stat/r",
		}},
	}

	// Construct message
	msg := sendables.Message{
		TextContent: &textContent,
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

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	// TODO Save stats
	return nil
}

func (tg *TelegramBot) settingsHandler(c tb.Context) error {
	message := c.Message()
	user := tg.Cache.FindUser(fmt.Sprint(message.Chat.ID), "tg")

	if !PreHandler(tg, user, c) {
		return nil
	}

	text := "🚀 *LaunchBot* | *User settings*\n" +
		"Use these settings to choose the launches you subscribe to, " +
		"like SpaceX or Rocket Lab, and when you receive notifications.\n\n" +
		"You can also set your time zone, so all dates and times are in your correct local time."

	subscribeButton := tb.InlineButton{
		Text: "🔔 Subscription settings",
		Data: "set/sub/view",
	}

	tzButton := tb.InlineButton{
		Text: "🌍 Set your time zone",
		Data: "set/tz/view",
	}

	exitButton := tb.InlineButton{
		Text: "✅ Exit settings",
		Data: "set/exit",
	}

	// Construct the keyboard and send-options
	kb := [][]tb.InlineButton{{subscribeButton}, {tzButton}, {exitButton}}

	text = utils.PrepareInputForMarkdown(text, "text")

	// Construct message
	msg := sendables.Message{
		TextContent: &text,
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

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	return nil
}

// Handler for settings callback requests
func settingsCallbackHandler(cb *tb.Callback, user *users.User, tg *TelegramBot) string {
	callbackData := strings.Split(cb.Data, "/")

	switch callbackData[1] {
	case "main": // User requested main settings menu
		text := "🚀 *LaunchBot* | *User settings*\n" +
			"Use these settings to choose the launches you subscribe to, " +
			"like SpaceX or Rocket Lab, and when you receive notifications.\n\n" +
			"You can also set your time zone, so all dates and times are in your correct local time."

		text = utils.PrepareInputForMarkdown(text, "text")

		subscribeButton := tb.InlineButton{
			Text: "🔔 Subscription settings",
			Data: "set/sub/view",
		}

		tzButton := tb.InlineButton{
			Text: "🌍 Set your time zone",
			Data: "set/tz/view",
		}

		exitButton := tb.InlineButton{
			Text: "✅ Exit settings",
			Data: "set/exit",
		}

		// Construct the keyboard and send-options
		kb := [][]tb.InlineButton{{subscribeButton}, {tzButton}, {exitButton}}

		sendOptions := tb.SendOptions{
			ParseMode:             "MarkdownV2",
			DisableWebPagePreview: true,
			ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		}

		editCbMessage(tg, cb, text, sendOptions)
		return "⚙️ Loaded settings"
	case "tz":
		switch callbackData[2] {
		case "view":
			// Check what time zone information user has saved
			userTimeZone := user.SavedTimeZoneInfo()

			link := fmt.Sprintf("[a time zone database entry](%s)", utils.PrepareInputForMarkdown("https://en.wikipedia.org/wiki/List_of_tz_database_time_zones", "link"))
			text := "🌍 *LaunchBot* | *Time zone settings*\n" +
				"LaunchBot sets your time zone with the help of Telegram's location sharing feature.\n\n" +
				"This is entirely privacy preserving, as your exact location is not required. Only the general " +
				"location is stored in the form of LINKHERE, such as Europe/Berlin or America/Lima.\n\n" +
				fmt.Sprintf("*Your current time zone is: %s.* You can remove your time zone information from LaunchBot's server at any time.",
					userTimeZone,
				)

			text = utils.PrepareInputForMarkdown(text, "text")
			text = strings.ReplaceAll(text, "LINKHERE", link)

			// Construct the keyboard and send-options
			setBtn := tb.InlineButton{
				Text: "🌍 Begin time zone set-up",
				Data: "set/tz/begin",
			}

			delBtn := tb.InlineButton{
				Text: "❌ Delete your time zone",
				Data: "set/tz/del",
			}

			retBtn := tb.InlineButton{
				Text: "⬅️ Back to settings",
				Data: "set/main",
			}

			kb := [][]tb.InlineButton{{setBtn}, {delBtn}, {retBtn}}

			sendOptions := tb.SendOptions{
				ParseMode:             "MarkdownV2",
				DisableWebPagePreview: true,
				ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
			}

			editCbMessage(tg, cb, text, sendOptions)
			return "🌍 Loaded time zone settings"
		case "begin": // User requested time zone setup
			text := "🌍 *LaunchBot* | *Time zone set-up*\n" +
				"To complete the time zone setup, follow the instructions below using your phone:\n\n" +
				"*1.* Make sure you are *replying* to *this message!*\n" +
				"*2.* Tap 📎 next to the text field, then choose 📍 Location.\n" +
				"*3.* As a reply, send the bot a location that is in your time zone. This can be a different city, or even a different country!" +
				"\n\n*Note:* location sharing is not supported in Telegram Desktop, so use your phone or tablet!"

			text = utils.PrepareInputForMarkdown(text, "text")

			retBtn := tb.InlineButton{
				Text: "⬅️ Cancel set-up",
				Data: "set/tz/view",
			}

			kb := [][]tb.InlineButton{{retBtn}}

			sendOptions := tb.SendOptions{
				ParseMode:             "MarkdownV2",
				DisableWebPagePreview: true,
				ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
				Protected:             true,
			}

			// Capture the message ID of this setup message
			msg := editCbMessage(tg, cb, text, sendOptions)

			// Store in list of TZ setup messages
			tg.TZSetupMessages[int64(msg.ID)] = cb.Sender.ID

			return "🌍 Loaded time zone set-up"
		case "del":
			// Delete tz info, dump to disk
			user.DeleteTimeZone()
			tg.Db.SaveUser(user)

			text := "🌍 *LaunchBot* | *Time zone settings*\n" +
				"Your time zone information was successfully deleted! " +
				fmt.Sprintf("Your current time zone is: *%s.*", user.SavedTimeZoneInfo())

			text = utils.PrepareInputForMarkdown(text, "text")

			retBtn := tb.InlineButton{
				Text: "⬅️ Back to settings",
				Data: "set/main",
			}

			kb := [][]tb.InlineButton{{retBtn}}

			sendOptions := tb.SendOptions{
				ParseMode:             "MarkdownV2",
				DisableWebPagePreview: true,
				ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
			}

			editCbMessage(tg, cb, text, sendOptions)

			return "🌍 Deleted time zone information"
		}
	}

	return ""
}

func (tg *TelegramBot) callbackHandler(c tb.Context) error {
	// Pointer to received callback
	cb := c.Callback()

	// User
	user := tg.Cache.FindUser(fmt.Sprint(cb.Message.Chat.ID), "tg")

	// Enforce rate-limits
	if !PreHandler(tg, user, c) {
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
			case "0":
				cbRespStr = "↩️ Returned to beginning"
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
	case "stat":
		switch callbackData[1] {
		case "r":
			newText := tg.Stats.String()

			// Construct the keyboard and send-options
			kb := [][]tb.InlineButton{{
				tb.InlineButton{
					Text: "🔄 Refresh data",
					Data: "stat/r",
				}},
			}

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

			cbRespStr = "🔄 Refreshed stats"
		}
	case "set":
		cbRespStr = settingsCallbackHandler(cb, user, tg)
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

func editCbMessage(tg *TelegramBot, cb *tb.Callback, text string, sendOptions tb.SendOptions) *tb.Message {
	// Edit message
	msg, err := tg.Bot.Edit(cb.Message, text, &sendOptions)

	if err != nil {
		// If not recoverable, return
		if !handleTelegramError(err, tg) {
			return nil
		}
	}

	return msg
}

func (tg *TelegramBot) locationReplyHandler(c tb.Context) error {
	// If not a reply, return immediately
	if c.Message().ReplyTo == nil {
		log.Debug().Msg("Not a reply")
		return nil
	}

	// If it's a reply, verify it's a reply to a time zone setup message
	uid, ok := tg.TZSetupMessages[int64(c.Message().ReplyTo.ID)]

	if !ok {
		// If the message we are replying to is from LaunchBot, check the text content
		if c.Message().ReplyTo.Sender.ID == tg.Bot.Me.ID {
			// Verify the text content matches a tz setup message
			if strings.Contains(c.Message().ReplyTo.Text, "🌍 LaunchBot | Time zone set-up") {
				// If the chat is private, we can ignore the strict TZSetupMessages map
				if c.Chat().Type == tb.ChatPrivate {
					ok = true
				}
			}
		}

		if !ok {
			return nil
		}
	}

	// This is a reply to a tz setup message: verify sender == initiator
	if uid != c.Message().Sender.ID {
		log.Debug().Msg("Sender != initiator")
		return nil
	}

	// Check if message contains location information
	if c.Message().Location == (&tb.Location{}) {
		log.Debug().Msg("Message location is nil")
		return nil
	}

	// Extract lat and lng
	lat := c.Message().Location.Lat
	lng := c.Message().Location.Lng

	// Pull locale
	locale := latlong.LookupZoneName(float64(lat), float64(lng))

	if locale == "" {
		log.Error().Msgf("Coordinates %.4f, %.4f yielded an empty locale", lat, lng)
		return nil
	}

	// Save locale to user's struct
	chat := tg.Cache.FindUser(fmt.Sprint(c.Message().Chat.ID), "tg")
	chat.Locale = locale
	tg.Db.SaveUser(chat)

	log.Info().Msgf("Saved locale=%s for chat=%s", locale, chat.Id)

	// Notify user of success
	successText := "🌍 *LaunchBot* | *Time zone set-up*\n" +
		fmt.Sprintf("Time zone setup completed! Your time zone was set to *%s*.\n\n",
			chat.SavedTimeZoneInfo()) +
		"If you ever want to remove this, simply use the same menu as you did previously. Stopping the bot " +
		"also removes all your saved data."

	successText = utils.PrepareInputForMarkdown(successText, "text")

	retBtn := tb.InlineButton{
		Text: "⬅️ Back to settings",
		Data: "set/main",
	}

	kb := [][]tb.InlineButton{{retBtn}}

	// Construct message
	msg := sendables.Message{
		TextContent: &successText,
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

	sendable.Recipients.Add(chat, false)

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	return nil
}
