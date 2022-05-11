package bots

import (
	"errors"
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

const (
	startMessage = "🌟 *Welcome to LaunchBot!* LaunchBot is your one-stop shop into the world of rocket launches. Subscribe to the launches of your favorite " +
		"space agency, or follow that one rocket company you're a fan of.\n\n" +
		"🐙 *LaunchBot is open-source, 100 % free, and will never ask you for anything.* If you're a developer and want to see a new feature, " +
		"you can open a pull request in GITHUBLINK.\n\n" +
		"🌠 *To get started, you can subscribe to some notifications, or try out the commands.* If you have feedback or a request for improvement, " +
		"you can use the feedback command."

	startMessageGroupExtra = "\n\n👷 *Note for group admins!* To reduce spam, LaunchBot only responds to requests by admins. " +
		"LaunchBot can also automatically delete commands it won't reply to, if given the permission to delete messages. " +
		"If you'd like everyone to be able to send commands, just flip a switch in the settings!"

	settingsMainText = "*LaunchBot* | *User settings*\n" +
		"🔔 Subscription settings allow you to choose what launches you receive notifications for, " +
		"like SpaceX's or Rocket Lab's launches, and when you receive these notifications.\n\n" +
		"🌍 You can also set your time zone, so all dates and times are in your local time, instead of UTC+0."

	settingsSubscriptionText = "*LaunchBot* | *Subscription settings*\n" +
		"🔔 Launch notification settings allow you to subscribe to entire countries' notifications, or just one launch provider like SpaceX.\n\n" +
		"⏰ You can also choose when you receive notifications, from four different time instances."

	notificationSettingsByCountryCode = "🔔 *LaunchBot* | *Notification settings*\n" +
		"You can search for specific launch-providers with the country flags, or simply enable notifications for all launch providers.\n\n" +
		"As an example, SpaceX can be found under the 🇺🇸-flag, and ISRO can be found under 🇮🇳-flag."
)

// Map a boolean status to a bell
var statusBell = map[bool]string{
	true: "✅", false: "🔕",
}

var boolToStringToggle = map[bool]string{
	true: "0", false: "1",
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

	var err error
	tg.Bot, err = tb.NewBot(tb.Settings{
		Token:  token,
		Poller: &tb.LongPoller{Timeout: time.Second * 60},
		Client: &http.Client{Timeout: time.Second * 60},
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Error creating Telegram bot")
	}

	// Set-up command handlers
	tg.Bot.Handle("/start", tg.startHandler)
	tg.Bot.Handle("/next", tg.nextHandler)
	tg.Bot.Handle("/schedule", tg.scheduleHandler)
	tg.Bot.Handle("/statistics", tg.statsHandler)
	tg.Bot.Handle("/settings", tg.settingsHandler)

	// Handle callbacks
	tg.Bot.Handle(tb.OnCallback, tg.callbackHandler)

	// Callback buttons that are handled directly
	// TODO handle schedule, stats, settings callbacks this way
	tg.Bot.Handle(&tb.InlineButton{Unique: "countryCodeView"}, tg.countryCodeListCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "notificationToggle"}, tg.notificationToggleCallback)

	// Handle incoming locations for time zone setup messages
	tg.Bot.Handle(tb.OnLocation, tg.locationReplyHandler)

	// Catch service messages as they happen
	tg.Bot.Handle(tb.OnMigration, tg.migrationHandler)
	tg.Bot.Handle(tb.OnAddedToGroup, tg.startHandler)
	tg.Bot.Handle(tb.OnGroupCreated, tg.startHandler)
	tg.Bot.Handle(tb.OnSuperGroupCreated, tg.startHandler)
	tg.Bot.Handle(tb.OnMyChatMember, tg.botMemberChangeHandler)
}

func (tg *TelegramBot) startHandler(ctx tb.Context) error {
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	if !PreHandler(tg, chat, ctx) {
		return nil
	}

	var textContent string

	if ctx.Chat().Type == tb.ChatGroup || ctx.Chat().Type == tb.ChatSuperGroup {
		// If a group, add extra information for admins
		textContent = utils.PrepareInputForMarkdown(startMessage+startMessageGroupExtra, "text")
	} else {
		// Otherwise, use the standard message format
		textContent = utils.PrepareInputForMarkdown(startMessage, "text")
	}

	// Set the Github link
	link := utils.PrepareInputForMarkdown("https://github.com/499602D2/tg-launchbot", "link")
	linkText := utils.PrepareInputForMarkdown("LaunchBot's GitHub repository", "text")
	textContent = strings.ReplaceAll(textContent, "GITHUBLINK", fmt.Sprintf("[*%s*](%s)", linkText, link))

	// Set buttons
	settingsBtn := tb.InlineButton{
		Text: "🔔 Go to notification settings",
		Data: "set/main/newMessage",
	}

	kb := [][]tb.InlineButton{{settingsBtn}}

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

	// Add the user
	sendable.Recipients.Add(chat, false)

	// Add to queue as a high-priority message
	go tg.Queue.Enqueue(&sendable, tg, true)

	// Check if chat is new
	if chat.Stats.SentCommands == 0 {
		log.Debug().Msgf("🌟 Bot added to a new chat! (id=%s)", chat.Id)

		if ctx.Chat().Type != tb.ChatPrivate {
			// Since the chat is new, get its member count
			memberCount, err := tg.Bot.Len(ctx.Chat())

			if err != nil {
				handleTelegramError(ctx, err, tg)
				return nil
			}

			chat.Stats.MemberCount = memberCount
			tg.Db.SaveUser(chat)
		}
	}

	// Update stats
	chat.Stats.SentCommands++

	return nil
}

// Handles the /schedule command
func (tg *TelegramBot) scheduleHandler(c tb.Context) error {
	user := tg.Cache.FindUser(fmt.Sprint(c.Chat().ID), "tg")

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

// Handles the /next command
func (tg *TelegramBot) nextHandler(c tb.Context) error {
	user := tg.Cache.FindUser(fmt.Sprint(c.Chat().ID), "tg")

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

// Handles the /stats command
func (tg *TelegramBot) statsHandler(c tb.Context) error {
	user := tg.Cache.FindUser(fmt.Sprint(c.Chat().ID), "tg")

	if !PreHandler(tg, user, c) {
		return nil
	}

	subscribers := tg.Db.GetSubscriberCount()
	textContent := tg.Stats.String(subscribers)

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

// Handles the /settings command
func (tg *TelegramBot) settingsHandler(c tb.Context) error {
	user := tg.Cache.FindUser(fmt.Sprint(c.Chat().ID), "tg")

	if !PreHandler(tg, user, c) {
		return nil
	}

	// Text content
	text := utils.PrepareInputForMarkdown(settingsMainText, "text")

	subscribeButton := tb.InlineButton{
		Text: "🔔 Subscription settings",
		Data: "set/sub/main",
	}

	tzButton := tb.InlineButton{
		Text: "🌍 Time zone settings",
		Data: "set/tz/main",
	}

	// Construct the keyboard and send-options
	kb := [][]tb.InlineButton{{subscribeButton}, {tzButton}}

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

// Handles requests to view a list of launch providers associated with a country code
func (tg *TelegramBot) countryCodeListCallback(c tb.Context) error {
	// Ensure callback data is valid
	data := strings.Split(c.Callback().Data, "/")

	if len(data) != 2 {
		err := errors.New(fmt.Sprintf("Got arbitrary data at cc/.. endpoint with length=%d", len(data)))
		log.Error().Err(err)
		return err
	}

	// Get chat
	user := tg.Cache.FindUser(fmt.Sprint(c.Chat().ID), "tg")

	// Status of all being enabled for this country code
	allEnabled := true

	// A dynamically generated keyboard array
	kb := [][]tb.InlineButton{}
	row := []tb.InlineButton{}

	// Country-code we want to view is at index 1: build the keyboard, and get status for all
	for i, id := range db.IdByCountryCode[data[1]] {
		enabled := user.GetNotificationStatusById(id)

		// If not enabled, set allEnabled to false
		if !enabled {
			allEnabled = false
		}

		row = append(row,
			tb.InlineButton{
				Unique: "notificationToggle",
				Text:   fmt.Sprintf("%s %s", statusBell[enabled], db.LSPShorthands[id].Name),
				Data:   fmt.Sprintf("id/%d/%s", id, map[bool]string{true: "0", false: "1"}[enabled]),
			})

		if len(row) == 2 || i == len(db.IdByCountryCode[data[1]])-1 {
			kb = append(kb, row)
			row = []tb.InlineButton{}
		}
	}

	// Add the return key
	kb = append(kb, []tb.InlineButton{{
		Text: "⬅️ Return",
		Data: "set/sub/bycountry",
	}})

	// Insert the toggle-all key at the beginning
	toggleAllBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   fmt.Sprintf("%s", map[bool]string{true: "🔕 Tap to disable all", false: "🔔 Tap to enable all"}[allEnabled]),
		Data:   fmt.Sprintf("cc/%s/%s", data[1], map[bool]string{true: "0", false: "1"}[allEnabled]),
	}

	// Insert at the beginning of the keyboard
	kb = append([][]tb.InlineButton{{toggleAllBtn}}, kb...)

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
	}

	// Edit message
	text := utils.PrepareInputForMarkdown(notificationSettingsByCountryCode, "text")
	editCbMessage(tg, c.Callback(), text, sendOptions)

	// Create callback response
	cbResp := tb.CallbackResponse{
		CallbackID: c.Callback().ID,
		Text:       fmt.Sprintf("Loaded %s", db.CountryCodeToName[data[1]]),
	}

	// Respond to callback
	err := tg.Bot.Respond(c.Callback(), &cbResp)

	if err != nil {
		log.Error().Err(err).Msg("Error responding to callback")
		handleTelegramError(c, err, tg)
	}

	return nil
}

// Handles callbacks related to toggling notification settings
func (tg *TelegramBot) notificationToggleCallback(c tb.Context) error {
	// Callback is of form (id, cc, all, time)/(id, cc, time-type, all-state)/(id-state, cc-state, time-state)
	data := strings.Split(c.Callback().Data, "/")

	// Get chat
	user := tg.Cache.FindUser(fmt.Sprint(c.Chat().ID), "tg")

	// Map a string 0/1 to a boolean status
	boolFlag := map[string]bool{
		"0": false, "1": true,
	}

	switch data[0] {
	case "all":
		user.SetAllFlag(boolFlag[data[1]])
		c.Callback().Data = "set/sub/bycountry"

	case "id":
		user.ToggleIdSubscription([]string{data[1]}, boolFlag[data[2]])
		intId, _ := strconv.Atoi(data[1])
		c.Callback().Data = fmt.Sprintf("cc/%s", db.LSPShorthands[intId].Cc)

	case "cc":
		// Convert all IDs associated with this country code to strings
		ids := []string{}
		for _, id := range db.IdByCountryCode[data[1]] {
			ids = append(ids, fmt.Sprint(id))
		}

		// Toggle all IDs
		user.ToggleIdSubscription(ids, boolFlag[data[2]])
		c.Callback().Data = fmt.Sprintf("cc/%s", data[1])

	case "time":
		// User is toggling a notification receive time
		user.SetNotificationTimeFlag(data[1], boolFlag[data[2]])
		c.Callback().Data = "set/sub/times"

	default:
		log.Warn().Msgf("Received arbitrary data in notificationToggle: %s", c.Callback().Data)
		return errors.New("Received arbitrary data")
	}

	// Save user in a go-routine
	go tg.Db.SaveUser(user)

	// Update view depending on input
	if data[0] == "all" || data[0] == "time" {
		cbRespText, _ := settingsCallbackHandler(c.Callback(), user, tg)

		// Create callback response
		cbResp := tb.CallbackResponse{
			CallbackID: c.Callback().ID,
			Text:       cbRespText,
		}

		// Respond to callback
		err := tg.Bot.Respond(c.Callback(), &cbResp)

		if err != nil {
			log.Error().Err(err).Msg("Error responding to callback")
			handleTelegramError(nil, err, tg)
		}
	} else {
		_ = tg.countryCodeListCallback(c)
	}

	return nil
}

// Handler for settings callback requests
func settingsCallbackHandler(cb *tb.Callback, user *users.User, tg *TelegramBot) (string, bool) {
	callbackData := strings.Split(cb.Data, "/")

	// TODO use the "Unique" property of inline buttons to do better callback handling

	switch callbackData[1] {
	case "main": // User requested main settings menu
		// Text content
		text := utils.PrepareInputForMarkdown(settingsMainText, "text")

		subscribeButton := tb.InlineButton{
			Text: "🔔 Subscription settings",
			Data: "set/sub/main",
		}

		tzButton := tb.InlineButton{
			Text: "🌍 Time zone settings",
			Data: "set/tz/main",
		}

		// Construct the keyboard and send-options
		kb := [][]tb.InlineButton{{subscribeButton}, {tzButton}}

		sendOptions := tb.SendOptions{
			ParseMode:             "MarkdownV2",
			DisableWebPagePreview: true,
			ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		}

		if len(callbackData) == 3 && callbackData[2] == "newMessage" {
			// If a new message is requested, wrap into a sendable and send as new
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
			go tg.Queue.Enqueue(&sendable, tg, true)
		} else {
			editCbMessage(tg, cb, text, sendOptions)
		}

		return "⚙️ Loaded settings", false
	case "tz":
		switch callbackData[2] {
		case "main":
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
			return "🌍 Loaded time zone settings", false
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

			return "🌍 Loaded time zone set-up", false
		case "del":
			// Delete tz info, dump to disk
			user.DeleteTimeZone()
			tg.Db.SaveUser(user)

			text := "🌍 *LaunchBot* | *Time zone settings*\n" +
				"Your time zone information was successfully deleted! " +
				fmt.Sprintf("Your new time zone is: *%s.*", user.SavedTimeZoneInfo())

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

			return "✅ Successfully deleted your time zone information", true
		}
	case "sub": // User requested subscription settings
		switch callbackData[2] {
		case "main":
			// Text content
			text := utils.PrepareInputForMarkdown(settingsSubscriptionText, "text")

			// Construct the keyboard and send-options
			subBtn := tb.InlineButton{
				Text: "🔔 Subscribe to launches",
				Data: "set/sub/bycountry",
			}

			timeBtn := tb.InlineButton{
				Text: "⏰ Adjust notification times",
				Data: "set/sub/times",
			}

			retBtn := tb.InlineButton{
				Text: "⬅️ Back to settings",
				Data: "set/main",
			}

			kb := [][]tb.InlineButton{{subBtn}, {timeBtn}, {retBtn}}

			sendOptions := tb.SendOptions{
				ParseMode:             "MarkdownV2",
				DisableWebPagePreview: true,
				ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
			}

			editCbMessage(tg, cb, text, sendOptions)
			return "🔔 Loaded subscription settings", false
		case "times":
			text := "⏰ *LaunchBot* | *Notification time settings*\n" +
				"Notifications are delivered 24 hours, 12 hours, 60 minutes, and 5 minutes before a launch.\n\n" +
				"By default, you will receive a notification 24 hours before, and 5 minutes before a launch. You can adjust this behavior here.\n\n" +
				"You can also toggle postpone notifications, which are sent when a launch has its launch time moved (if a notification has already been sent)."

			text = utils.PrepareInputForMarkdown(text, "text")

			// Construct the keyboard and send-options
			time24hBtn := tb.InlineButton{
				Unique: "notificationToggle",
				Text:   fmt.Sprintf("%s 24-hour", map[bool]string{true: "✅", false: "🔕"}[user.Enabled24h]),
				Data:   fmt.Sprintf("time/24h/%s", boolToStringToggle[user.Enabled24h]),
			}

			time12hBtn := tb.InlineButton{
				Unique: "notificationToggle",
				Text:   fmt.Sprintf("%s 12-hour", map[bool]string{true: "✅", false: "🔕"}[user.Enabled12h]),
				Data:   fmt.Sprintf("time/12h/%s", boolToStringToggle[user.Enabled12h]),
			}

			time1hBtn := tb.InlineButton{
				Unique: "notificationToggle",
				Text:   fmt.Sprintf("%s 1-hour", map[bool]string{true: "✅", false: "🔕"}[user.Enabled1h]),
				Data:   fmt.Sprintf("time/1h/%s", boolToStringToggle[user.Enabled1h]),
			}

			time5minBtn := tb.InlineButton{
				Unique: "notificationToggle",
				Text:   fmt.Sprintf("%s 5-minute", map[bool]string{true: "✅", false: "🔕"}[user.Enabled5min]),
				Data:   fmt.Sprintf("time/5min/%s", boolToStringToggle[user.Enabled5min]),
			}

			postponeBtn := tb.InlineButton{
				Unique: "notificationToggle",
				Text:   fmt.Sprintf("%s Postponements", map[bool]string{true: "✅", false: "🔕"}[user.EnabledPostpone]),
				Data:   fmt.Sprintf("time/postpone/%s", boolToStringToggle[user.EnabledPostpone]),
			}

			retBtn := tb.InlineButton{
				Text: "⬅️ Return",
				Data: "set/sub/main",
			}

			kb := [][]tb.InlineButton{{time24hBtn, time12hBtn}, {time1hBtn, time5minBtn}, {postponeBtn}, {retBtn}}

			sendOptions := tb.SendOptions{
				ParseMode:             "MarkdownV2",
				DisableWebPagePreview: true,
				ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
			}

			editCbMessage(tg, cb, text, sendOptions)
			return "⏲️ Loaded notification time settings", false
		case "bycountry": // Dynamically generated notification preferences
			// Pull user's current notification preferences (we get lists of IDs)
			// enabled, disabled := user.GetNotificationStates()

			// Map boolean status toggle to a string status
			// As in, if currently enabled, the new status is disabled
			strStatus := map[bool]string{true: "0", false: "1"}

			// The SubscribedAll flag can be set, alongside with the user having _some_ flags disabled.
			// Ensure user has no flags disabled.
			allEnabled := false
			if len(user.UnsubscribedFrom) != 0 {
				allEnabled = false
			} else {
				allEnabled = user.SubscribedAll
			}

			toggleAllBtn := tb.InlineButton{
				Unique: "notificationToggle",
				Text:   map[bool]string{true: "🔕 Tap to disable all", false: "🔔 Tap to enable all"}[allEnabled],
				Data:   fmt.Sprintf("all/%s", strStatus[allEnabled]),
			}

			// A dynamically generated keyboard array
			kb := [][]tb.InlineButton{{toggleAllBtn}}
			row := []tb.InlineButton{}

			// Generate the keyboard dynamically from available country-codes
			for i, countryCode := range db.CountryCodes {
				row = append(row,
					tb.InlineButton{
						Unique: "countryCodeView",
						Text:   db.CountryCodeToName[countryCode],
						Data:   fmt.Sprintf("cc/%s", countryCode),
					})

				if len(row) == 2 || i == len(db.CountryCodes)-1 {
					kb = append(kb, row)
					row = []tb.InlineButton{}
				}
			}

			// Add the return key
			kb = append(kb, []tb.InlineButton{{
				Text: "⬅️ Return",
				Data: "set/sub/main",
			}})

			text := utils.PrepareInputForMarkdown(notificationSettingsByCountryCode, "text")

			sendOptions := tb.SendOptions{
				ParseMode:             "MarkdownV2",
				DisableWebPagePreview: true,
				ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
			}

			editCbMessage(tg, cb, text, sendOptions)
			return "🔔 Notification settings loaded", false
		}
	}

	return "", false
}

// A catch-all type callback handler
// TODO: handle each callback type individually
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
		sent, err := tg.Bot.Edit(cb.Message, newText, &sendOptions)

		if err != nil {
			if !handleSendError(sent, err, tg) {
				return nil
			}
		}
	case "nxt":
		// Get new text for the refresh
		idx, err := strconv.Atoi(callbackData[2])

		if err != nil {
			log.Error().Err(err).Msgf("Unable to convert nxt/r cbdata to int: %s", callbackData[2])
			idx = 0
		}

		newText, keyboard := tg.Cache.LaunchListMessage(user, idx, true)

		// Send options: reuse keyboard
		sendOptions := tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: keyboard,
		}

		// Edit message
		sent, err := tg.Bot.Edit(cb.Message, newText, &sendOptions)

		if err != nil {
			// If not recoverable, return
			if !handleSendError(sent, err, tg) {
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
		sent, err := tg.Bot.Edit(cb.Message, newText, &sendOptions)

		if err != nil {
			// If not recoverable, return
			if !handleSendError(sent, err, tg) {
				return nil
			}
		}
	case "stat":
		switch callbackData[1] {
		case "r":
			newText := tg.Stats.String(tg.Db.GetSubscriberCount())

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
			sent, err := tg.Bot.Edit(cb.Message, newText, &sendOptions)

			if err != nil {
				// If not recoverable, return
				if !handleSendError(sent, err, tg) {
					return nil
				}
			}

			cbRespStr = "🔄 Refreshed stats"
		}
	case "set":
		cbRespStr, showAlert = settingsCallbackHandler(cb, user, tg)
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
		handleTelegramError(nil, err, tg)
	}

	return nil
}

func editCbMessage(tg *TelegramBot, cb *tb.Callback, text string, sendOptions tb.SendOptions) *tb.Message {
	// Edit message
	msg, err := tg.Bot.Edit(cb.Message, text, &sendOptions)

	if err != nil {
		// If not recoverable, return
		if !handleSendError(msg, err, tg) {
			return nil
		}
	}

	return msg
}

// Handles locations that the bot receives in a chat
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

	// Delete the setup message
	err := tg.Bot.Delete(tb.Editable(c.Message().ReplyTo))

	if err != nil {
		if !handleTelegramError(nil, err, tg) {
			log.Warn().Msg("Deleting time zone setup message failed")
		}
	}

	return nil
}

// Handles migration service messages
func (tg *TelegramBot) migrationHandler(ctx tb.Context) error {
	from, to := ctx.Migration()
	log.Info().Msgf("Chat upgraded to a supergroup: migrating chat from %d to %d...", from, to)

	tg.Db.MigrateGroup(from, to, "tg")
	return nil
}

// Handles changes related to the bot's member status in a chat
func (tg *TelegramBot) botMemberChangeHandler(ctx tb.Context) error {
	// Chat associated with this update
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	// If we were kicked or somehow managed to leave the chat, remove the chat from the db
	if ctx.ChatMember().NewChatMember.Role == tb.Kicked || ctx.ChatMember().NewChatMember.Role == tb.Left {
		log.Info().Msgf("Kicked or left from chat=%s, deleting from database...", chat.Id)
		tg.Db.RemoveUser(chat)
	}

	return nil
}
