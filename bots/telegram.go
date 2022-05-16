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
	"time"

	"github.com/bradfitz/latlong"
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
	Stats        *stats.Statistics
	Keyboard     Keyboard
	Owner        int64
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

	// Init keyboard-holder struct
	tg.Keyboard = Keyboard{Settings: Settings{}, Command: Command{}}

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
	// TODO remove before production
	tg.Bot.Handle("/send", tg.fauxNotificationSender)

	// Handle callbacks by button-type
	tg.Bot.Handle(&tb.InlineButton{Unique: "next"}, tg.nextHandler)
	tg.Bot.Handle(&tb.InlineButton{Unique: "schedule"}, tg.scheduleHandler)
	tg.Bot.Handle(&tb.InlineButton{Unique: "stats"}, tg.statsHandler)
	tg.Bot.Handle(&tb.InlineButton{Unique: "settings"}, tg.settingsCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "countryCodeView"}, tg.countryCodeListCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "notificationToggle"}, tg.notificationToggleCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "muteToggle"}, tg.muteCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "expand"}, tg.expandMessageContent)

	// Handle incoming locations for time zone setup messages
	tg.Bot.Handle(tb.OnLocation, tg.locationReplyHandler)

	// Catch service messages as they happen
	tg.Bot.Handle(tb.OnMigration, tg.migrationHandler)
	tg.Bot.Handle(tb.OnAddedToGroup, tg.startHandler)
	tg.Bot.Handle(tb.OnGroupCreated, tg.startHandler)
	tg.Bot.Handle(tb.OnSuperGroupCreated, tg.startHandler)
	tg.Bot.Handle(tb.OnMyChatMember, tg.botMemberChangeHandler)
}

// Handle the /start command and events where the bot is added to a new chat
func (tg *TelegramBot) startHandler(ctx tb.Context) error {
	// Load chat
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	adminOnlyCommand := true
	if !PreHandler(tg, chat, ctx, 2, adminOnlyCommand, true, "start") {
		return nil
	}

	var textContent string

	if isGroup(ctx.Chat().Type) {
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

	// Load send-options
	sendOptions, _ := tg.Keyboard.Command.Start()

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type: "command",
		Message: &sendables.Message{
			TextContent: textContent,
			SendOptions: sendOptions,
		},
	}

	// Add the user
	sendable.AddRecipient(chat, false)

	// Add to queue as a high-priority message
	go tg.Queue.Enqueue(&sendable, tg, true)

	// Check if chat is new
	if chat.Stats.SentCommands == 0 {
		log.Debug().Msgf("🌟 Bot added to a new chat! (id=%s)", chat.Id)

		if ctx.Chat().Type != tb.ChatPrivate {
			// For new group chats (or channels), get their member count
			memberCount, err := tg.Bot.Len(ctx.Chat())

			if err != nil {
				log.Error().Err(err).Msg("Loading chat's member-count failed")
				handleTelegramError(ctx, err, tg)
				return nil
			}

			// Save member-count (for private chats, the default is already 1)
			chat.Stats.MemberCount = memberCount - 1
			tg.Db.SaveUser(chat)
		}
	}

	// Update stats
	chat.Stats.SentCommands++

	return nil
}

// Handle feedback
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
func (tg *TelegramBot) scheduleHandler(ctx tb.Context) error {
	// Load chat
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	// Request is a command if the callback is nil
	isCommand := (ctx.Callback() == nil)

	adminOnlyCommand := false
	if !PreHandler(tg, chat, ctx, 2, adminOnlyCommand, isCommand, "schedule") {
		return nil
	}

	// The mode to use, either "v" for vehicles, or "m" for missions
	var mode string

	if isCommand {
		// If a command, use the default vehicle mode
		mode = "v"
	} else {
		// Otherwise, we're doing a callback: get the requested mode
		cbData := strings.Split(ctx.Callback().Data, "/")
		mode = cbData[2]
	}

	// Get text for the message
	scheduleMsg := tg.Cache.ScheduleMessage(chat, mode == "m")
	sendOptions, _ := tg.Keyboard.Command.Schedule(mode)

	if isCommand {
		// Construct message
		msg := sendables.Message{
			TextContent: scheduleMsg,
			SendOptions: sendOptions,
		}

		// Wrap into a sendable
		sendable := sendables.Sendable{
			Type:    "command",
			Message: &msg,
		}

		// Add to send queue as high-priority
		sendable.AddRecipient(chat, false)
		go tg.Queue.Enqueue(&sendable, tg, true)
	} else {
		tg.editCbMessage(ctx.Callback(), scheduleMsg, sendOptions)
		return tg.respondToCallback(ctx, "🔄 Schedule loaded", false)
	}

	return nil
}

// Handles the /next command
func (tg *TelegramBot) nextHandler(ctx tb.Context) error {
	// Load chat
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	// Request is a command if the callback is nil
	isCommand := (ctx.Callback() == nil)
	cbData := []string{}

	adminOnlyCommand := false
	if !PreHandler(tg, chat, ctx, 2, adminOnlyCommand, isCommand, "next") {
		return nil
	}

	// Index we're loading the launch at
	index := 0

	if !isCommand {
		// For callbacks, load the index the user is requesting
		var err error
		cbData = strings.Split(ctx.Callback().Data, "/")
		index, err = strconv.Atoi(cbData[2])

		if err != nil {
			log.Error().Err(err).Msgf("Could not convert %s to int in /next", ctx.Callback().Data)
		}
	}

	// Get text, send-options for the message
	textContent := tg.Cache.NextLaunchMessage(chat, index)
	sendOptions, _ := tg.Keyboard.Command.Next(index, len(tg.Cache.Launches))

	if isCommand {
		// Construct message
		msg := sendables.Message{
			TextContent: textContent,
			AddUserTime: true,
			RefTime:     tg.Cache.Launches[0].NETUnix,
			SendOptions: sendOptions,
		}

		// Wrap into a sendable
		sendable := sendables.Sendable{
			Type:    "command",
			Message: &msg,
		}

		// Add to send queue as high-priority
		go tg.Queue.Enqueue(&sendable, tg, true)
		sendable.AddRecipient(chat, false)

		return nil
	}

	// Create callback response text
	var cbResponse string

	switch cbData[1] {
	case "r":
		cbResponse = "🔄 Data refreshed"
	case "n":
		// Create callback response text
		switch cbData[3] {
		case "+":
			cbResponse = "Next launch ➡️"
		case "-":
			cbResponse = "⬅️ Previous launch"
		case "0":
			cbResponse = "↩️ Returned to beginning"
		default:
			log.Error().Msgf("Undefined behavior for callbackData in nxt/n (cbd[3]=%s)", cbData[3])
			cbResponse = "⚠️ Please do not send arbitrary data to the bot"
		}
	}

	// Edit message, respond to callback
	tg.editCbMessage(ctx.Callback(), textContent, sendOptions)
	return tg.respondToCallback(ctx, cbResponse, false)
}

// Handles the /stats command
func (tg *TelegramBot) statsHandler(ctx tb.Context) error {
	// Load chat
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	// Request is a command if the callback is nil
	isCommand := (ctx.Callback() == nil)

	adminOnlyCommand := false
	if !PreHandler(tg, chat, ctx, 1, adminOnlyCommand, isCommand, "stats") {
		return nil
	}

	// Reload some statistics
	tg.Stats.DbSize = tg.Db.Size
	subscribers := tg.Db.GetSubscriberCount()

	// Get text content
	textContent := tg.Stats.String(subscribers)

	// Get keyboard
	sendOptions, _ := tg.Keyboard.Command.Statistics()

	// If a command, throw the message into the queue
	if isCommand {
		// Wrap into a sendable
		sendable := sendables.Sendable{
			Type: "command",
			Message: &sendables.Message{
				TextContent: textContent,
				SendOptions: sendOptions,
			},
		}

		sendable.AddRecipient(chat, false)

		// Add to send queue as high-priority
		go tg.Queue.Enqueue(&sendable, tg, true)
		return nil
	}

	// Otherwise it's a callback request: update text, respond to callback
	tg.editCbMessage(ctx.Callback(), textContent, sendOptions)
	return tg.respondToCallback(ctx, "🔄 Refreshed stats", false)
}

// Handles the /settings command
func (tg *TelegramBot) settingsHandler(ctx tb.Context) error {
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	adminOnlyCommand := true
	if !PreHandler(tg, chat, ctx, 1, adminOnlyCommand, true, "settings") {
		return nil
	}

	// Load keyboard
	_, kb := tg.Keyboard.Settings.Main(isGroup(ctx.Chat().Type))

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

	sendable.AddRecipient(chat, false)

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
	sendOptions, _ := tg.Keyboard.Settings.Subscription.ByCountryCode(chat, data[1])

	// Edit message
	text := utils.PrepareInputForMarkdown(notificationSettingsByCountryCode, "text")
	tg.editCbMessage(ctx.Callback(), text, sendOptions)

	// Respond to callback
	_ = tg.respondToCallback(ctx, fmt.Sprintf("Loaded %s", db.CountryCodeToName[data[1]]), false)

	return nil
}

// Handles callbacks related to toggling notification settings
func (tg *TelegramBot) notificationToggleCallback(ctx tb.Context) error {
	// Callback is of form (id, cc, all, time)/(id, cc, time-type, all-state)/(id-state, cc-state, time-state)
	data := strings.Split(ctx.Callback().Data, "/")
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	adminOnlyCallback := true
	if !PreHandler(tg, chat, ctx, 1, adminOnlyCallback, false, "settings") {
		return tg.respondToCallback(ctx, interactionNotAllowed, true)
	}

	// Variable for updated keyboard following a callback
	var (
		cbText          string
		updatedKeyboard [][]tb.InlineButton
	)

	switch data[0] {
	case "all":
		// Toggle all-flag
		toggleTo := utils.BinStringStateToBool[data[1]]
		chat.SetAllFlag(toggleTo)

		// Update keyboard
		_, updatedKeyboard = tg.Keyboard.Settings.Subscription.Main(chat)

		// Callback response
		cbText = fmt.Sprintf("%s all notifications", utils.NotificationToggleCallbackString(toggleTo))

	case "id":
		// Toggle subscription for this ID
		toggleTo := utils.BinStringStateToBool[data[2]]
		chat.ToggleIdSubscription([]string{data[1]}, toggleTo)

		// Load updated keyboard
		intId, _ := strconv.Atoi(data[1])

		// Update keyboard
		_, updatedKeyboard = tg.Keyboard.Settings.Subscription.ByCountryCode(chat, db.LSPShorthands[intId].Cc)

		// Callback response
		cbText = fmt.Sprintf("%s %s", utils.NotificationToggleCallbackString(toggleTo), db.LSPShorthands[intId].Name)

	case "cc":
		// Load all IDs associated with this country-code
		toggleTo := utils.BinStringStateToBool[data[2]]
		ids := db.AllIdsByCountryCode(data[1])

		// Toggle all IDs
		chat.ToggleIdSubscription(ids, toggleTo)

		// Update keyboard
		_, updatedKeyboard = tg.Keyboard.Settings.Subscription.ByCountryCode(chat, data[1])

		// Callback response
		cbText = fmt.Sprintf("%s all for %s", utils.NotificationToggleCallbackString(toggleTo), db.CountryCodeToName[data[1]])

	case "time":
		if len(data) < 3 {
			log.Warn().Msgf("Insufficient data in time/ toggle endpoint: %d", len(data))
			return nil
		}

		// User is toggling a notification receive time
		toggleTo := utils.BinStringStateToBool[data[2]]
		chat.SetNotificationTimeFlag(data[1], toggleTo)

		// Update keyboard
		_, updatedKeyboard = tg.Keyboard.Settings.Notifications(chat)

		// Callback response
		cbText = fmt.Sprintf("%s %s notifications", utils.NotificationToggleCallbackString(toggleTo), data[1])

	case "cmd":
		if len(data) < 3 {
			log.Warn().Msgf("Insufficient data in cmd/ toggle endpoint: %d", len(data))
			return nil
		}

		// Toggle a command status
		toggleTo := utils.BinStringStateToBool[data[2]]
		chat.ToggleCommandPermissionStatus(data[1], toggleTo)

		// Update keyboard
		_, updatedKeyboard = tg.Keyboard.Settings.Group(chat)

		// Callback response
		cbText = fmt.Sprintf("%s permission status", utils.NotificationToggleCallbackString(toggleTo))

	default:
		log.Warn().Msgf("Received arbitrary data in notificationToggle: %s", ctx.Callback().Data)
		return errors.New("Received arbitrary data")
	}

	// Save user in a go-routine
	go tg.Db.SaveUser(chat)

	// Update the keyboard, as the state was modified
	modified, err := tg.Bot.EditReplyMarkup(ctx.Message(), &tb.ReplyMarkup{InlineKeyboard: updatedKeyboard})

	if err != nil {
		handleSendError(ctx.Chat().ID, modified, err, tg)
	}

	// Respond to callback
	return tg.respondToCallback(ctx, cbText, false)
}

// Handle launch mute/unmute callbacks
func (tg *TelegramBot) muteCallback(ctx tb.Context) error {
	// Data is in the format mute/id/toggleTo/notificationType
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")
	data := strings.Split(ctx.Callback().Data, "/")

	adminOnlyCallback := true
	if !PreHandler(tg, chat, ctx, 1, adminOnlyCallback, false, "mute") {
		return tg.respondToCallback(ctx, interactionNotAllowed, true)
	}

	if len(data) != 4 {
		log.Warn().Msgf("Got invalid data at /mute endpoint with length=%d from chat=%s", len(data), chat.Id)
		return errors.New("Invalid data at /mute endpoint")
	}

	// Get bool state the mute status will be toggled to
	toggleTo := utils.BinStringStateToBool[data[2]]

	// Toggle user's mute status (id, newState)
	success := chat.ToggleLaunchMute(data[1], toggleTo)

	// On success, save to disk
	if success {
		go tg.Db.SaveUser(chat)
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
			if !handleSendError(ctx.Chat().ID, modified, err, tg) {
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
		sendOptions, _ := tg.Keyboard.Settings.Main(isGroup(cb.Message.Chat.Type))

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
				handleSendError(ctx.Chat().ID, modified, err, tg)
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
			tg.editCbMessage(cb, text, sendOptions)
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
			sendOptions, _ := tg.Keyboard.Settings.TimeZone.Main()

			tg.editCbMessage(cb, text, sendOptions)
			return tg.respondToCallback(ctx, "🌍 Loaded time zone settings", false)

		case "begin": // User requested time zone setup
			// Message text, keyboard
			text := utils.PrepareInputForMarkdown(settingsTzSetup, "text")
			sendOptions, _ := tg.Keyboard.Settings.TimeZone.Setup()

			// Edit message
			tg.editCbMessage(cb, text, sendOptions)
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

			tg.editCbMessage(cb, text, sendOptions)
			return tg.respondToCallback(ctx, "✅ Successfully deleted your time zone information!", true)
		}

	case "sub":
		// User requested subscription settings
		switch callbackData[2] {
		case "times":
			// Text content, send-options with the keyboard
			text := utils.PrepareInputForMarkdown(settingsNotificationTimes, "text")
			sendOptions, _ := tg.Keyboard.Settings.Notifications(chat)

			tg.editCbMessage(cb, text, sendOptions)
			return tg.respondToCallback(ctx, "⏲️ Loaded notification time settings", false)
		case "bycountry":
			// Dynamically generated notification preferences
			sendOptions, _ := tg.Keyboard.Settings.Subscription.Main(chat)
			text := utils.PrepareInputForMarkdown(notificationSettingsByCountryCode, "text")

			tg.editCbMessage(cb, text, sendOptions)
			return tg.respondToCallback(ctx, "🔔 Notification settings loaded", false)
		}

	case "group":
		// Group-specific settings
		text := settingsGroupMain

		text = utils.PrepareInputForMarkdown(text, "text")
		sendOptions, _ := tg.Keyboard.Settings.Group(chat)

		// Capture the message ID of this setup message
		tg.editCbMessage(cb, text, sendOptions)
		return tg.respondToCallback(ctx, "👷 Loaded group settings", false)
	}

	return nil
}

// Handle notification message expansions
func (tg *TelegramBot) expandMessageContent(ctx tb.Context) error {
	// Pointer to received callback
	cb := ctx.Callback()

	// User
	chat := tg.Cache.FindUser(fmt.Sprint(cb.Message.Chat.ID), "tg")

	// Split data field
	callbackData := strings.Split(cb.Data, "/")

	// Extract ID and notification type
	launchId := callbackData[1]
	notifType := callbackData[2]

	// Find launch by ID (it may not exist in the cache anymore)
	launch, err := tg.Cache.FindLaunchById(launchId)

	if err != nil {
		cbRespStr := fmt.Sprintf("⚠️ %s", err.Error())
		return tg.respondToCallback(ctx, cbRespStr, true)
	}

	// Get text for this launch
	newText := launch.NotificationMessage(notifType, true)
	newText = sendables.SetTime(newText, chat, launch.NETUnix, true, false, false)

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
		if !handleSendError(ctx.Chat().ID, sent, err, tg) {
			return nil
		}
	}

	return tg.respondToCallback(ctx, "ℹ️ Notification expanded", false)
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

// Edit a message following a callback, and handle any errors
func (tg *TelegramBot) editCbMessage(cb *tb.Callback, text string, sendOptions tb.SendOptions) *tb.Message {
	// Edit message
	msg, err := tg.Bot.Edit(cb.Message, text, &sendOptions)

	if err != nil {
		// If not recoverable, return
		if !handleSendError(cb.Message.Chat.ID, msg, err, tg) {
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
		recoverable := handleTelegramError(nil, err, tg)

		if recoverable {
			log.Error().Err(err).Msg("Recoverable error when responding to callback")
		}
	}

	return err
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

// Test notification sends
func (tg *TelegramBot) fauxNotificationSender(ctx tb.Context) error {
	// Admin-only function
	if ctx.Message().Sender.ID != tg.Owner {
		log.Error().Msgf("/test called by non-admin (%d)", ctx.Message().Sender.ID)
		return nil
	}

	// Load user from cache
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Message().Sender.ID), "tg")

	// Create message, get notification type
	testId := ctx.Data()

	if len(testId) == 0 {
		sendable := sendables.TextOnlySendable("No launch ID entered", chat)
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
		Unique: "expand",
		Text:   "ℹ️ Expand description",
		Data:   fmt.Sprintf("exp/%s/%s", launch.Id, notifType),
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

// Return a chat user's admin status
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
func (tg *TelegramBot) UpdateStats(chat *users.User, isCommand bool) {
	if isCommand {
		chat.Stats.SentCommands++
		tg.Stats.Commands++
	} else {
		chat.Stats.SentCallbacks++
		tg.Stats.Callbacks++
	}
}
