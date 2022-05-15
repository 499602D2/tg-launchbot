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

// TODO
// - ensure all callback data is valid (length checks)
// - use bot.EditReplyMarkup instead of bot.Edit wherever possible

type TelegramBot struct {
	Bot          *tb.Bot
	Db           *db.Database
	Cache        *db.Cache
	Queue        *Queue
	HighPriority *HighPriorityQueue
	Spam         *AntiSpam
	Stats        *stats.Statistics
	Owner        int64
}

type HighPriorityQueue struct {
	HasItemsInQueue bool
	Queue           []*sendables.Sendable
	Mutex           sync.Mutex
}

// Constant message text contents
const (
	startMessage = "🌟 *Welcome to LaunchBot!* LaunchBot is your one-stop shop into the world of rocket launches. Subscribe to the launches of your favorite " +
		"space agency, or follow that one rocket company you're a fan of.\n\n" +
		"🐙 *LaunchBot is open-source, 100 % free, and respects your privacy.* If you're a developer and want to see a new feature, " +
		"you can open a pull request in GITHUBLINK.\n\n" +
		"🌠 *To get started, you can subscribe to some notifications, or try out the commands.* If you have any feedback, or a request for improvement, " +
		"you can use the feedback command."

	startMessageGroupExtra = "\n\n👷 *Note for group admins!* To reduce spam, LaunchBot only responds to requests by admins. " +
		"LaunchBot can also automatically delete commands it won't reply to, if given the permission to delete messages. " +
		"If you'd like everyone to be able to send commands, just flip a switch in the settings!"

	feedbackMessageText = "🌟 *LaunchBot* | *Developer feedback*\n" +
		"Here, you can send feedback that goes directly to the developer. To send feedback, just write a message that starts with /feedback!\n\n" +
		"An example would be `/feedback Great bot, thank you!`\n\n" +
		"*Thank you for using LaunchBot!*"

	feedbackReceivedText = "🌟 *Thank you for your feedback!* Your feedback was received successfully."

	// TODO add user's time zone
	settingsMainText = "*LaunchBot* | *User settings*\n" +
		"🚀 *Launch subscription settings* allow you to choose what launches you receive notifications for, like SpaceX's or NASA's.\n\n" +
		"⏰ *Notification settings* allow you to choose when you receive notifications.\n\n" +
		"🌍 *Time zone settings* let you set your time zone, so all dates and times are in your local time, instead of UTC+0."

	settingsMainGroupExtra = "\n\n👷 *Group settings* let admins change some group-specific settings, such as allowing all users to send commands."

	notificationSettingsByCountryCode = "🚀 *LaunchBot* | *Subscription settings*\n" +
		"You can search for specific launch-providers with the country flags, or simply enable notifications for all launch providers.\n\n" +
		"As an example, SpaceX can be found under the 🇺🇸-flag, and ISRO can be found under 🇮🇳-flag. You can also choose to enable all notifications."

	settingsNotificationTimes = "⏰ *LaunchBot* | *Notification time settings*\n" +
		"Notifications are delivered 24 hours, 12 hours, 60 minutes, and 5 minutes before a launch.\n\n" +
		"By default, you will receive a notification 24 hours before, and 5 minutes before a launch. You can adjust this behavior here.\n\n" +
		"You can also toggle postpone notifications, which are sent when a launch has its launch time moved (if a notification has already been sent)."

	settingsTzMain = "🌍 *LaunchBot* | *Time zone settings*\n" +
		"LaunchBot sets your time zone with the help of Telegram's location sharing feature.\n\n" +
		"This is entirely privacy preserving, as your exact location is not required. Only the general " +
		"location is stored in the form of LINKHERE, such as Europe/Berlin or America/Lima.\n\n" +
		"*Your current time zone is: USERTIMEZONE.* You can remove your time zone information from LaunchBot's server at any time."

	settingsTzSetup = "🌍 *LaunchBot* | *Time zone set-up*\n" +
		"To complete the time zone setup, follow the instructions below using your phone:\n\n" +
		"*1.* Make sure you are *replying* to *this message!*\n\n" +
		"*2.* Tap 📎 next to the text field, then choose `📍` `Location`.\n\n" +
		"*3.* As a reply, send the bot a location that is in your time zone. This can be a different city, or even a different country!" +
		"\n\n*Note:* location sharing is not supported in Telegram Desktop, so use your phone or tablet!"

	settingsGroupMain = "👷 *LaunchBot* | *Group settings*\n" +
		"These are LaunchBot's settings only available to groups, which will be expanded in the future. Currently, " +
		"they allow admins to enable command-access to all group participants."

	interactionNotAllowed = "⚠️ You're not allowed to do that"
)

// Simple method to initialize the TelegramBot object
func (tg *TelegramBot) Initialize(token string) {
	// Create primary Telegram queue
	tg.Queue = &Queue{
		Sendables: make(map[string]*sendables.Sendable),
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
	tg.Bot.Handle("/feedback", tg.feedbackHandler)

	// Handler for fake notification requests
	tg.Bot.Handle("/send", tg.fauxNotificationSender)

	// Handle callbacks
	tg.Bot.Handle(tb.OnCallback, tg.callbackHandler)

	// Callback buttons that are handled directly
	// TODO handle schedule, stats, settings callbacks this way
	tg.Bot.Handle(&tb.InlineButton{Unique: "countryCodeView"}, tg.countryCodeListCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "notificationToggle"}, tg.notificationToggleCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "muteToggle"}, tg.muteCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "settings"}, tg.settingsCallback)

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

	adminOnlyCommand := true
	if !PreHandler(tg, chat, ctx, 2, adminOnlyCommand, true, "start") {
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
		Unique: "settings",
		Text:   "⚙️ Go to LaunchBot settings",
		Data:   "set/main/newMessage",
	}

	kb := [][]tb.InlineButton{{settingsBtn}}

	msg := sendables.Message{
		TextContent: textContent,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type:    "command",
		Message: &msg,
	}

	// Add the user
	sendable.AddRecipient(chat, false)

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

			chat.Stats.MemberCount = memberCount - 1
			tg.Db.SaveUser(chat)
		}
	}

	// Update stats
	chat.Stats.SentCommands++

	return nil
}

func (tg *TelegramBot) feedbackHandler(ctx tb.Context) error {
	// Load user
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	adminOnlyCommand := true
	if !PreHandler(tg, chat, ctx, 1, adminOnlyCommand, true, "feedback") {
		return nil
	}

	// If the command has no parameters, send instruction message
	if len(strings.Split(ctx.Data(), " ")) == 1 {
		log.Debug().Msgf("Chat=%s requested feedback instructions", chat.Id)
		text := utils.PrepareInputForMarkdown(feedbackMessageText, "text")
		go tg.Queue.Enqueue(sendables.TextOnlySendable(text, chat), tg, true)

		return nil
	}

	// Command has parameters: log feedback, send to owner
	feedbackLog := fmt.Sprintf("✍️ *Got feedback from %s:* %s", chat.Id, ctx.Data())
	log.Info().Msgf(feedbackLog)

	go tg.Queue.Enqueue(sendables.TextOnlySendable(
		utils.PrepareInputForMarkdown(feedbackLog, "text"),
		tg.Cache.FindUser(fmt.Sprint(tg.Owner), "tg")),
		tg, true,
	)

	// Send a message confirming we received the feedback
	newText := utils.PrepareInputForMarkdown(feedbackReceivedText, "text")
	go tg.Queue.Enqueue(sendables.TextOnlySendable(newText, chat), tg, true)

	return nil
}

// Handles the /schedule command
func (tg *TelegramBot) scheduleHandler(c tb.Context) error {
	user := tg.Cache.FindUser(fmt.Sprint(c.Chat().ID), "tg")

	adminOnlyCommand := false
	if !PreHandler(tg, user, c, 2, adminOnlyCommand, true, "schedule") {
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
		TextContent: scheduleMsg,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type:    "command",
		Message: &msg,
	}

	sendable.AddRecipient(user, false)

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	// TODO Save stats
	return nil
}

// Handles the /next command
func (tg *TelegramBot) nextHandler(ctx tb.Context) error {
	user := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	adminOnlyCommand := false
	if !PreHandler(tg, user, ctx, 2, adminOnlyCommand, true, "next") {
		return nil
	}

	// Get text for the message
	textContent, _ := tg.Cache.LaunchListMessage(user, 0, false)

	refreshBtn := tb.InlineButton{
		Text: "Refresh 🔄",
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
		TextContent: textContent,
		AddUserTime: true,
		RefTime:     tg.Cache.Launches[0].NETUnix,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type:    "command",
		Message: &msg,
	}

	sendable.AddRecipient(user, false)

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	// TODO Save stats
	return nil
}

// Handles the /stats command
func (tg *TelegramBot) statsHandler(c tb.Context) error {
	user := tg.Cache.FindUser(fmt.Sprint(c.Chat().ID), "tg")

	adminOnlyCommand := false
	if !PreHandler(tg, user, c, 1, adminOnlyCommand, true, "stats") {
		return nil
	}

	subscribers := tg.Db.GetSubscriberCount()
	textContent := tg.Stats.String(subscribers)

	// Construct the keyboard and send-options
	kb := [][]tb.InlineButton{{
		tb.InlineButton{
			Text: "🔄 Refresh data",
			Data: "stats/r",
		}},
	}

	// Construct message
	msg := sendables.Message{
		TextContent: textContent,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type:    "command",
		Message: &msg,
	}

	sendable.AddRecipient(user, false)

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	// TODO Save stats
	return nil
}

// Handles the /settings command
func (tg *TelegramBot) settingsHandler(ctx tb.Context) error {
	user := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	adminOnlyCommand := true
	if !PreHandler(tg, user, ctx, 1, adminOnlyCommand, true, "settings") {
		return nil
	}

	// Load keyboard
	_, kb := KbSettingsMain(isGroup(ctx.Chat().Type))

	// Init text so we don't need to run it twice thorugh the markdown escaper
	var text string

	// If chat is a group, show the group-specific settings
	if isGroup(ctx.Chat().Type) {
		text = utils.PrepareInputForMarkdown(settingsMainText+settingsMainGroupExtra, "text")
	} else {
		// Not a group, so use the standard text
		text = utils.PrepareInputForMarkdown(settingsMainText, "text")
	}

	// Construct message
	msg := sendables.Message{
		TextContent: text,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type:    "command",
		Message: &msg,
	}

	sendable.AddRecipient(user, false)

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	return nil
}

// Handles requests to view a list of launch providers associated with a country code
func (tg *TelegramBot) countryCodeListCallback(ctx tb.Context) error {
	// Ensure callback data is valid
	data := strings.Split(ctx.Callback().Data, "/")

	if len(data) != 2 {
		err := errors.New(fmt.Sprintf("Got arbitrary data at cc/.. endpoint with length=%d", len(data)))
		log.Error().Err(err)
		return err
	}

	// Get chat
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	adminOnlyCallback := true
	if !PreHandler(tg, chat, ctx, 1, adminOnlyCallback, false, "settings") {
		return tg.respondToCallback(ctx, interactionNotAllowed, true)
	}

	// Get send-options
	sendOptions, _ := KbSubscriptionByCc(chat, data[1])

	// Edit message
	text := utils.PrepareInputForMarkdown(notificationSettingsByCountryCode, "text")
	editCbMessage(tg, ctx.Callback(), text, sendOptions)

	// Respond to callback
	_ = tg.respondToCallback(ctx, fmt.Sprintf("Loaded %s", db.CountryCodeToName[data[1]]), false)

	return nil
}

// Handles callbacks related to toggling notification settings
func (tg *TelegramBot) notificationToggleCallback(ctx tb.Context) error {
	// Callback is of form (id, cc, all, time)/(id, cc, time-type, all-state)/(id-state, cc-state, time-state)
	data := strings.Split(ctx.Callback().Data, "/")
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	// Take zero tokens for this callback, as reloading the settings menu already takes the tokens
	adminOnlyCallback := true
	if !PreHandler(tg, chat, ctx, 1, adminOnlyCallback, false, "settings") {
		return tg.respondToCallback(ctx, interactionNotAllowed, true)
	}

	// Variable for updated keyboard following a callback
	var updatedKeyboard [][]tb.InlineButton

	switch data[0] {
	case "all":
		// Toggle all-flag
		chat.SetAllFlag(utils.BinStringStateToBool[data[1]])

		// Update keyboard
		_, updatedKeyboard = KbSubscriptionMainSettings(chat)

	case "id":
		// Toggle subscription for this ID
		chat.ToggleIdSubscription([]string{data[1]}, utils.BinStringStateToBool[data[2]])

		// Load updated keyboard
		intId, _ := strconv.Atoi(data[1])

		// Update keyboard
		_, updatedKeyboard = KbSubscriptionByCc(chat, db.LSPShorthands[intId].Cc)

	case "cc":
		// Load all IDs associated with this country-code
		ids := db.AllIdsByCountryCode(data[1])

		// Toggle all IDs
		chat.ToggleIdSubscription(ids, utils.BinStringStateToBool[data[2]])

		// Update keyboard
		_, updatedKeyboard = KbSubscriptionByCc(chat, data[1])

	case "time":
		if len(data) < 3 {
			log.Warn().Msgf("Insufficient data in time/ toggle endpoint: %d", len(data))
			return nil
		}

		// User is toggling a notification receive time
		chat.SetNotificationTimeFlag(data[1], utils.BinStringStateToBool[data[2]])

		// Update keyboard
		_, updatedKeyboard = KbNotificationSettings(chat)

	case "cmd":
		if len(data) < 3 {
			log.Warn().Msgf("Insufficient data in cmd/ toggle endpoint: %d", len(data))
			return nil
		}

		// Toggle a command status
		chat.ToggleCommandPermissionStatus(data[1], utils.BinStringStateToBool[data[2]])

		// Update keyboard
		_, updatedKeyboard = KbGroupSettings(chat)

	default:
		log.Warn().Msgf("Received arbitrary data in notificationToggle: %s", ctx.Callback().Data)
		return errors.New("Received arbitrary data")
	}

	// Save user in a go-routine
	go tg.Db.SaveUser(chat)

	// Update the keyboard, as the state was modified
	modified, err := tg.Bot.EditReplyMarkup(ctx.Message(), &tb.ReplyMarkup{InlineKeyboard: updatedKeyboard})

	if err != nil {
		handleSendError(modified, err, tg)
	}

	return nil
}

// Handle launch mute/unmute callbacks
func (tg *TelegramBot) muteCallback(ctx tb.Context) error {
	// Data is in the format mute/id/toggleTo/notificationType
	user := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")
	data := strings.Split(ctx.Callback().Data, "/")

	adminOnlyCallback := true
	if !PreHandler(tg, user, ctx, 1, adminOnlyCallback, false, "mute") {
		return tg.respondToCallback(ctx, interactionNotAllowed, true)
	}

	if len(data) != 4 {
		log.Warn().Msgf("Got invalid data at /mute endpoint with length=%d from chat=%s", len(data), user.Id)
		return errors.New("Invalid data at /mute endpoint")
	}

	// Get bool state the mute status will be toggled to
	toggleTo := utils.BinStringStateToBool[data[2]]

	// Toggle user's mute status (id, newState)
	success := user.ToggleLaunchMute(data[1], toggleTo)

	// On success, save to disk
	if success {
		go tg.Db.SaveUser(user)
	}

	cbResponseText := ""

	if success {
		// Set mute button according to the new state
		muteBtn := tb.InlineButton{
			Unique: "muteToggle",
			Text:   map[bool]string{true: "🔊 Unmute launch", false: "🔇 Mute launch"}[toggleTo],
			Data:   fmt.Sprintf("mute/%s/%s/%s", data[1], utils.ToggleBoolStateAsString[toggleTo], data[3]),
		}

		// Set the existing mute button to the new one (always at zeroth index, regardless of expansion status)
		ctx.Message().ReplyMarkup.InlineKeyboard[0] = []tb.InlineButton{muteBtn}

		// Edit message's reply markup, since we don't need to touch the message content itself
		modified, err := tg.Bot.EditReplyMarkup(ctx.Message(), &tb.ReplyMarkup{InlineKeyboard: ctx.Message().ReplyMarkup.InlineKeyboard})

		if err != nil {
			// If not recoverable, return
			if !handleSendError(modified, err, tg) {
				return nil
			}
		}

		if toggleTo == true {
			cbResponseText = "🔇 Launch muted!"
		} else {
			cbResponseText = "🔊 Launch unmuted! You will now receive notifications for this launch."
		}
	} else {
		cbResponseText = "⚠️ Request failed! This issue has been noted."
	}

	// Create callback response
	cbResp := tb.CallbackResponse{
		CallbackID: ctx.Callback().ID,
		Text:       cbResponseText,
		ShowAlert:  true,
	}

	// Respond to callback
	err := tg.Bot.Respond(ctx.Callback(), &cbResp)

	if err != nil {
		handleTelegramError(ctx, err, tg)
	}

	return nil
}

// Handler for settings callback requests. Returns a callback response and showAlert bool.
func (tg *TelegramBot) settingsCallback(ctx tb.Context) error {
	// Load chat
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	// Ensure callback sender is an admin
	adminOnlyCallback := true
	if !PreHandler(tg, chat, ctx, 1, adminOnlyCallback, false, "settings") {
		return tg.respondToCallback(ctx, interactionNotAllowed, true)
	}

	// Split data into an array
	cb := ctx.Callback()
	callbackData := strings.Split(cb.Data, "/")

	// TODO use the "Unique" property of inline buttons to do better callback handling

	switch callbackData[1] {
	case "main": // User requested main settings menu
		// Load keyboard
		sendOptions, _ := KbSettingsMain(isGroup(cb.Message.Chat.Type))

		// Init text so we don't need to run it twice thorugh the markdown escaper
		var text string

		// If chat is a group, show the group-specific settings
		if isGroup(cb.Message.Chat.Type) {
			text = utils.PrepareInputForMarkdown(settingsMainText+settingsMainGroupExtra, "text")
		} else {
			// Not a group, so use the standard text
			text = utils.PrepareInputForMarkdown(settingsMainText, "text")
		}

		if len(callbackData) == 3 && callbackData[2] == "newMessage" {
			// Remove the keyboard button from the start message
			modified, err := tg.Bot.EditReplyMarkup(ctx.Message(), &tb.ReplyMarkup{})

			if err != nil {
				handleSendError(modified, err, tg)
			}

			// If a new message is requested, wrap into a sendable and send as new
			msg := sendables.Message{
				TextContent: text,
				SendOptions: sendOptions,
			}

			// Wrap into a sendable
			sendable := sendables.Sendable{
				Type:    "command",
				Message: &msg,
			}

			sendable.AddRecipient(chat, false)
			go tg.Queue.Enqueue(&sendable, tg, true)
		} else {
			editCbMessage(tg, cb, text, sendOptions)
		}

		return tg.respondToCallback(ctx, "⚙️ Loaded settings", false)

	case "tz":
		switch callbackData[2] {
		case "main":
			// Message text: add user time zone
			text := strings.ReplaceAll(settingsTzMain, "USERTIMEZONE", chat.SavedTimeZoneInfo())
			text = utils.PrepareInputForMarkdown(text, "text")

			// Set link
			link := fmt.Sprintf("[a time zone database entry](%s)",
				utils.PrepareInputForMarkdown("https://en.wikipedia.org/wiki/List_of_tz_database_time_zones", "link"))
			text = strings.ReplaceAll(text, "LINKHERE", link)

			// Load keyboard
			sendOptions, _ := KbTzMain()

			editCbMessage(tg, cb, text, sendOptions)
			return tg.respondToCallback(ctx, "🌍 Loaded time zone settings", false)

		case "begin": // User requested time zone setup
			// Message text, keyboard
			text := utils.PrepareInputForMarkdown(settingsTzSetup, "text")
			sendOptions, _ := KbTzSetup()

			// Edit message
			editCbMessage(tg, cb, text, sendOptions)
			return tg.respondToCallback(ctx, "🌍 Loaded time zone set-up", false)

		case "del":
			// Delete tz info, dump to disk
			chat.DeleteTimeZone()
			tg.Db.SaveUser(chat)

			text := "🌍 *LaunchBot* | *Time zone settings*\n" +
				"Your time zone information was successfully deleted! " +
				fmt.Sprintf("Your new time zone is: *%s.*", chat.SavedTimeZoneInfo())

			text = utils.PrepareInputForMarkdown(text, "text")

			retBtn := tb.InlineButton{
				Unique: "settings",
				Text:   "⬅️ Back to settings",
				Data:   "set/main",
			}

			kb := [][]tb.InlineButton{{retBtn}}

			sendOptions := tb.SendOptions{
				ParseMode:             "MarkdownV2",
				DisableWebPagePreview: true,
				ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
			}

			editCbMessage(tg, cb, text, sendOptions)
			return tg.respondToCallback(ctx, "✅ Successfully deleted your time zone information!", true)
		}
	case "sub":
		// User requested subscription settings
		switch callbackData[2] {
		case "times":
			// Text content, send-options with the keyboard
			text := utils.PrepareInputForMarkdown(settingsNotificationTimes, "text")
			sendOptions, _ := KbNotificationSettings(chat)

			editCbMessage(tg, cb, text, sendOptions)
			return tg.respondToCallback(ctx, "⏲️ Loaded notification time settings", false)
		case "bycountry":
			// Dynamically generated notification preferences
			sendOptions, _ := KbSubscriptionMainSettings(chat)
			text := utils.PrepareInputForMarkdown(notificationSettingsByCountryCode, "text")

			editCbMessage(tg, cb, text, sendOptions)
			return tg.respondToCallback(ctx, "🔔 Notification settings loaded", false)
		}
	case "group":
		// Group-specific settings
		text := settingsGroupMain

		text = utils.PrepareInputForMarkdown(text, "text")
		sendOptions, _ := KbGroupSettings(chat)

		// Capture the message ID of this setup message
		editCbMessage(tg, cb, text, sendOptions)
		return tg.respondToCallback(ctx, "👷 Loaded group settings", false)
	}

	return nil
}

// A catch-all type callback handler
// TODO: handle each callback type individually
func (tg *TelegramBot) callbackHandler(ctx tb.Context) error {
	// Pointer to received callback
	cb := ctx.Callback()

	// User
	chat := tg.Cache.FindUser(fmt.Sprint(cb.Message.Chat.ID), "tg")

	// Split data field
	callbackData := strings.Split(cb.Data, "/")
	primaryRequest := callbackData[0]

	// Ensure callback sender has permission
	if !PreHandler(tg, chat, ctx, 1, false, false, primaryRequest) {
		return tg.respondToCallback(ctx, interactionNotAllowed, true)
	}

	// Callback response
	var cbRespStr string

	// Toggle to show a persistent alert for errors
	showAlert := false

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
		newText := tg.Cache.ScheduleMessage(chat, callbackData[2] == "m")

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

		newText, keyboard := tg.Cache.LaunchListMessage(chat, idx, true)

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
				cbRespStr = "Next ➡️"
			case "-":
				cbRespStr = "⬅️ Previous"
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
		newText = sendables.SetTime(newText, chat, launch.NETUnix, true, false)

		// Load mute status
		muted := chat.HasMutedLaunch(launch.Id)

		// Add mute button
		muteBtn := tb.InlineButton{
			Unique: "muteToggle",
			Text:   map[bool]string{true: "🔊 Unmute launch", false: "🔇 Mute launch"}[muted],
			Data:   fmt.Sprintf("mute/%s/%s/%s", launch.Id, utils.ToggleBoolStateAsString[muted], notifType),
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
	case "stats":
		switch callbackData[1] {
		case "r":
			newText := tg.Stats.String(tg.Db.GetSubscriberCount())

			// Construct the keyboard and send-options
			kb := [][]tb.InlineButton{{
				tb.InlineButton{
					Text: "🔄 Refresh data",
					Data: "stats/r",
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

// Edit a message following a callback, and handle any errors
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

// Responds to a callback with text, show alert if configured
func (tg *TelegramBot) respondToCallback(ctx tb.Context, text string, showAlert bool) error {
	// Create callback response
	cbResp := tb.CallbackResponse{
		CallbackID: ctx.Callback().ID,
		Text:       text,
		ShowAlert:  showAlert,
	}

	// Respond to callback
	err := tg.Bot.Respond(ctx.Callback(), &cbResp)

	if err != nil {
		log.Error().Err(err).Msg("Error responding to callback")
		handleTelegramError(nil, err, tg)
		return err
	}

	return nil
}

// Attempt deleting the message associated with a context
func (tg *TelegramBot) tryRemovingMessage(ctx tb.Context) {
	// Get bot's member status
	bot, err := tg.Bot.ChatMemberOf(ctx.Chat(), tg.Bot.Me)

	if err != nil {
		log.Error().Msg("Loading bot's permissions in chat failed")
		handleTelegramError(ctx, err, tg)
		return
	}

	if bot.CanDeleteMessages {
		// If we have permission to delete messages, delete the command message
		err = tg.Bot.Delete(ctx.Message())
	} else {
		// If You're not allowed to do that, return
		log.Debug().Msgf("Cannot delete messages in chat=%d", ctx.Chat().ID)
		return
	}

	// Check errors
	if err != nil {
		log.Error().Msg("Deleting message sent by a non-admin failed")
		handleTelegramError(ctx, err, tg)
		return
	}

	log.Debug().Msgf("Deleted message by non-admin in chat=%d", ctx.Chat().ID)
}

// Handles locations that the bot receives in a chat
func (tg *TelegramBot) locationReplyHandler(ctx tb.Context) error {
	// Verify sender is an admin
	if !tg.senderIsAdmin(ctx) {
		log.Debug().Msg("Location sender is not an admin")
		return nil
	}

	// If not a reply, return immediately
	if ctx.Message().ReplyTo == nil {
		log.Debug().Msg("Received a location, but it's not a reply")
		return nil
	}

	// If the message we are replying to is from LaunchBot, check the text content
	if ctx.Message().ReplyTo.Sender.ID == tg.Bot.Me.ID {
		// Verify the text content matches a tz setup message
		if !strings.Contains(ctx.Message().ReplyTo.Text, "🌍 LaunchBot | Time zone set-up") {
			log.Debug().Msg("Location reply to a message that is not a tz setup message")
			return nil
		}
	} else {
		log.Debug().Msgf("Not a reply to LaunchBot's message")
		return nil
	}

	// Check if message contains location information
	if ctx.Message().Location == (&tb.Location{}) {
		log.Debug().Msg("Message location is nil")
		return nil
	}

	// Extract lat and lng
	lat := ctx.Message().Location.Lat
	lng := ctx.Message().Location.Lng

	// Pull locale
	locale := latlong.LookupZoneName(float64(lat), float64(lng))

	if locale == "" {
		log.Error().Msgf("Coordinates %.4f, %.4f yielded an empty locale", lat, lng)
		return errors.New("Coordinates yielded an empty locale")
	}

	// Save locale to user's struct
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Message().Chat.ID), "tg")
	chat.Locale = locale
	tg.Db.SaveUser(chat)

	log.Info().Msgf("Saved locale=%s for chat=%s", locale, chat.Id)

	// Notify user of success
	successText := "🌍 *LaunchBot* | *Time zone set-up*\n" +
		"Time zone setup completed! Your time zone was set to *USERTIMEZONE*.\n\n" +
		"If you ever want to remove this, simply use the same menu as you did previously. Stopping the bot " +
		"also removes all your saved data."

	successText = strings.ReplaceAll(successText, "USERTIMEZONE", chat.SavedTimeZoneInfo())
	successText = utils.PrepareInputForMarkdown(successText, "text")

	retBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "⬅️ Back to settings",
		Data:   "set/main",
	}

	kb := [][]tb.InlineButton{{retBtn}}

	// Construct message
	msg := sendables.Message{
		TextContent: successText,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type:    "command",
		Message: &msg,
	}

	sendable.AddRecipient(chat, false)

	// Add to send queue as high-priority
	go tg.Queue.Enqueue(&sendable, tg, true)

	// Delete the setup message
	err := tg.Bot.Delete(tb.Editable(ctx.Message().ReplyTo))

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

// Test notification sends
func (tg *TelegramBot) fauxNotificationSender(ctx tb.Context) error {
	// Admin-only function
	if ctx.Message().Sender.ID != tg.Owner {
		log.Error().Msgf("/test called by non-admin (%d)", ctx.Message().Sender.ID)
		return nil
	}

	// Load user from cache
	user := tg.Cache.FindUser(fmt.Sprint(ctx.Message().Sender.ID), "tg")

	// Create message, get notification type
	testId := ctx.Data()

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
		Unique: "muteToggle",
		Text:   "🔇 Mute launch",
		Data:   fmt.Sprintf("mute/%s/1/%s", launch.Id, notifType),
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
		TextContent: text,
		AddUserTime: true,
		RefTime:     launch.NETUnix,
		SendOptions: tb.SendOptions{
			ParseMode:   "MarkdownV2",
			ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// Add recipients
	platform := "tg"
	subscribers := launch.NotificationRecipients(tg.Db, notifType, platform)

	// Create sendable
	sendable := sendables.Sendable{
		Type: "notification", NotificationType: notifType,
		LaunchId: launch.Id,
		Message:  &msg, Recipients: subscribers,
	}

	// Add to send queue as a high-priority message
	go tg.Queue.Enqueue(&sendable, tg, false)

	return nil
}

// Return user's admin status
// FUTURE: cache, and keep track of member status changes as they happen
func (tg *TelegramBot) senderIsAdmin(ctx tb.Context) bool {
	// Load member
	member, err := tg.Bot.ChatMemberOf(ctx.Chat(), ctx.Sender())

	if err != nil {
		log.Error().Err(err).Msg("Getting ChatMemberOf() failed in isAdmin")
		return false
	}

	// Return true if user is admin or creator
	return member.Role == tb.Administrator || member.Role == tb.Creator
}

// Stat updater for pre-handler: update the field according to cmd/cb
func (tg *TelegramBot) UpdateStats(user *users.User, isCommand bool) {
	if isCommand {
		user.Stats.SentCommands++
		tg.Stats.Commands++
	} else {
		user.Stats.SentCallbacks++
		tg.Stats.Callbacks++
	}
}
