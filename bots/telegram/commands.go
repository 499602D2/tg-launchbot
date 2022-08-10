package telegram

import (
	"errors"
	"fmt"
	"launchbot/db"
	"launchbot/logs"
	"launchbot/sendables"
	"launchbot/utils"
	"strconv"
	"strings"

	"github.com/bradfitz/latlong"
	"github.com/dustin/go-humanize"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// Admin-only command to e.g. dump logs and database remotely
func (tg *Bot) adminCommand(ctx tb.Context) error {
	// Owner-only function
	if !tg.senderIsOwner(ctx) {
		log.Error().Msgf("/admin called by non-owner (%d in %d)", ctx.Sender().ID, ctx.Chat().ID)
		return nil
	}

	text := fmt.Sprintf("🤖 *LaunchBot admin-panel*\n"+
		"Cached launches: %d\n"+
		"Cached users: %d\n\n"+
		"Send in progress: %v\n"+
		"Log-file size: %s",
		len(tg.Cache.Launches),
		len(tg.Cache.Users.InCache),
		tg.Spam.NotificationSendUnderway,
		humanize.Bytes(uint64(logs.GetLogSize(""))),
	)

	text = utils.PrepareInputForMarkdown(text, "text")
	sendOptions, _ := tg.Template.Keyboard.Command.Admin()

	if ctx.Callback() != nil {
		tg.editCbMessage(ctx.Callback(), text, sendOptions)
		return tg.respondToCallback(ctx, "🔄 Data refreshed", false)
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type: sendables.Command,
		Message: &sendables.Message{
			TextContent: text,
			SendOptions: sendOptions,
		},
	}

	// Add the user
	sendable.AddRecipient(tg.Cache.FindUser(fmt.Sprint(tg.Owner), "tg"), false)

	// Add to queue as a high-priority message
	tg.Enqueue(&sendable, true)

	return nil
}

// Admin-only command to respond to feedback messages (Simple direct messages to users)
func (tg *Bot) adminReply(ctx tb.Context) error {
	// Owner-only function
	if !tg.senderIsOwner(ctx) {
		log.Error().Msgf("/admin called by non-owner (%d in %d)", ctx.Sender().ID, ctx.Chat().ID)
		return nil
	}

	// Split data
	inputDataSplit := strings.Split(ctx.Text(), " ")

	if len(inputDataSplit) == 1 {
		tg.Enqueue(sendables.TextOnlySendable(
			utils.PrepareInputForMarkdown("Incorrect data length. Format: /reply [userId] [text...]", "text"),
			tg.Cache.FindUser(fmt.Sprint(tg.Owner), "tg")),
			true,
		)

		return nil
	}

	text := fmt.Sprintf(
		"📟 *Received a feedback response*\n\n"+
			"%s\n\n"+
			"_To respond to this message, use /feedback again._",
		strings.Join(inputDataSplit[2:], " "),
	)

	tg.Enqueue(sendables.TextOnlySendable(
		utils.PrepareInputForMarkdown(text, "italictext"),
		tg.Cache.FindUser(inputDataSplit[1], "tg")),
		true,
	)

	log.Debug().Msgf("Sent feedback response to user=%s", strings.Split(ctx.Text(), " ")[1])

	// Send confirmation message to admin
	tg.Enqueue(sendables.TextOnlySendable(
		utils.PrepareInputForMarkdown(
			fmt.Sprintf("📟 Response sent to *%s*", inputDataSplit[1]), "text"),
		tg.Cache.FindUser(fmt.Sprint(tg.Owner), "tg")),
		true,
	)

	return nil
}

// Handles the /start command when called directly
func (tg *Bot) permissionedStart(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, true, "start")

	if err != nil {
		log.Warn().Msg("Running permissionedStart failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	return tg.unpermissionedStart(ctx)
}

// Handle events where the bot is added to a new chat, i.e. cases where the
// command does not require permissions to be interacted with
func (tg *Bot) unpermissionedStart(ctx tb.Context) error {
	// Load chat
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	// Get text for message
	textContent := tg.Template.Messages.Command.Start(isGroup(ctx.Chat()))
	textContent = utils.PrepareInputForMarkdown(textContent, "text")

	// Set the Github link
	link := utils.PrepareInputForMarkdown("https://github.com/499602D2/tg-launchbot", "link")
	linkText := utils.PrepareInputForMarkdown("LaunchBot's GitHub repository.", "text")
	textContent = strings.ReplaceAll(textContent, "GITHUBLINK", fmt.Sprintf("[*%s*](%s)", linkText, link))

	// Load send-options
	sendOptions, _ := tg.Template.Keyboard.Command.Start()

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type: sendables.Command,
		Message: &sendables.Message{
			TextContent: textContent,
			SendOptions: sendOptions,
		},
	}

	// Disable notification for channels
	sendable.Message.SendOptions.DisableNotification = isChannel(ctx.Chat())

	// Add the user
	sendable.AddRecipient(chat, false)

	// Add to queue as a high-priority message
	tg.Enqueue(&sendable, true)

	// Check if chat is new
	if chat.Stats.SentCommands == 0 || chat.Stats.SentCommands == 1 {
		log.Debug().Msgf("🌟 Bot added to a new chat! (id=%s)", chat.Id)

		if ctx.Chat().Type != tb.ChatPrivate {
			// For new group chats (or channels), get their member count
			memberCount, err := tg.Bot.Len(ctx.Chat())

			if err != nil {
				log.Error().Err(err).Msg("Loading chat's member-count failed")
				tg.handleError(ctx, nil, err, ctx.Chat().ID)
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
func (tg *Bot) feedbackHandler(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, true, "feedback")

	if err != nil {
		log.Warn().Msg("Running feedbackHandler failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// If no command parameters, we're not receiving feedback
	receivingFeedback := len(strings.Split(ctx.Data(), " ")) > 1

	// Load message
	message := tg.Template.Messages.Command.Feedback(receivingFeedback)

	// If the command has no parameters, send instruction message
	if !receivingFeedback {
		log.Debug().Msgf("Chat=%s requested feedback instructions", chat.Id)
		text := utils.PrepareInputForMarkdown(message, "text")

		tg.Enqueue(sendables.TextOnlySendable(text, chat), true)
		return nil
	}

	// Command has parameters: log feedback, send to owner
	feedbackLog := fmt.Sprintf("✍️ *Got feedback from %s:* %s", chat.Id, ctx.Data())
	log.Info().Msgf(feedbackLog)

	tg.Enqueue(
		sendables.TextOnlySendable(
			utils.PrepareInputForMarkdown(feedbackLog, "text"),
			tg.Cache.FindUser(fmt.Sprint(tg.Owner), "tg")),
		true,
	)

	// Send a message confirming we received the feedback
	newText := utils.PrepareInputForMarkdown(message, "text")
	tg.Enqueue(sendables.TextOnlySendable(newText, chat), true)

	return nil
}

// Handles the /schedule command
func (tg *Bot) scheduleHandler(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, false, "schedule")

	if err != nil {
		log.Warn().Msg("Running scheduleHandler failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// The mode to use, either "v" for vehicles, or "m" for missions
	var mode string

	if interaction.IsCommand {
		// If a command, use the default vehicle mode
		mode = "v"
	} else {
		// Otherwise, we're doing a callback: get the requested mode
		mode = strings.Split(ctx.Callback().Data, "/")[1]
	}

	// Get text for the message
	scheduleMsg := tg.Cache.ScheduleMessage(chat, mode == "m", tg.Username)
	sendOptions, _ := tg.Template.Keyboard.Command.Schedule(mode)

	if interaction.IsCommand {
		// Construct message
		msg := sendables.Message{
			TextContent: scheduleMsg,
			SendOptions: sendOptions,
		}

		// Disable notification for channels
		msg.SendOptions.DisableNotification = isChannel(ctx.Chat())

		// Wrap into a sendable
		sendable := sendables.Sendable{
			Type:    sendables.Command,
			Message: &msg,
		}

		// Add to send queue as high-priority
		sendable.AddRecipient(chat, false)
		tg.Enqueue(&sendable, true)
	} else {
		tg.editCbMessage(ctx.Callback(), scheduleMsg, sendOptions)
		return tg.respondToCallback(ctx, "🔄 Schedule loaded", false)
	}

	return nil
}

// Handles the /next command
func (tg *Bot) nextHandler(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, false, "next")

	if err != nil {
		log.Warn().Msg("Running nextHandler failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// Index we're loading the launch at: defaults to 0 for commands
	index := 0
	cbData := []string{}

	if !interaction.IsCommand {
		// For callbacks, load the index the user is requesting
		var err error
		cbData = strings.Split(ctx.Callback().Data, "/")
		index, err = strconv.Atoi(cbData[1])

		if err != nil {
			log.Error().Err(err).Msgf("Could not convert %s to int in /next", ctx.Callback().Data)
		}
	}

	// Get text, send-options for the message
	textContent, cacheLength := tg.Cache.NextLaunchMessage(chat, index)

	if cacheLength == 0 {
		// If cache-length is zero, send a warning to the user
		sendable := sendables.TextOnlySendable(textContent, chat)
		tg.Enqueue(sendable, true)
		return nil
	}

	// Load the keyboard and the send-options
	sendOptions, _ := tg.Template.Keyboard.Command.Next(index, cacheLength)

	if interaction.IsCommand {
		// Construct message
		msg := sendables.Message{
			TextContent: textContent,
			AddUserTime: true,
			RefTime:     tg.Cache.Launches[0].NETUnix,
			SendOptions: sendOptions,
		}

		// Check if we need to send it silently
		msg.SendOptions.DisableNotification = isChannel(ctx.Chat())

		// Wrap into a sendable
		sendable := sendables.Sendable{
			Type:    sendables.Command,
			Message: &msg,
		}

		// Add to send queue as high-priority
		sendable.AddRecipient(chat, false)
		tg.Enqueue(&sendable, true)

		return nil
	}

	// Create callback response text
	var cbResponse string

	switch cbData[0] {
	case "r":
		cbResponse = "🔄 Data refreshed"
	case "n":
		// Create callback response text, depending on the direction
		switch cbData[2] {
		case "+":
			cbResponse = "Next launch ➡️"
		case "-":
			cbResponse = "⬅️ Previous launch"
		case "0":
			cbResponse = "↩️ Returned to beginning"
		default:
			log.Warn().Msgf("Undefined behavior for callbackData in /next (cbd[2]=%s)", cbData[2])
			cbResponse = "⚠️ Please do not send arbitrary data to the bot"
		}
	}

	// Edit message, respond to callback
	tg.editCbMessage(ctx.Callback(), textContent, sendOptions)
	return tg.respondToCallback(ctx, cbResponse, false)
}

// Handles the /stats command
func (tg *Bot) statsHandler(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, false, "stats")

	if err != nil {
		log.Warn().Msg("Running statsHandler failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// Reload some statistics
	tg.Stats.DbSize = tg.Db.Size
	tg.Stats.Subscribers = tg.Db.Subscribers
	tg.Stats.WeeklyActiveUsers = tg.Db.WeeklyActiveUsers

	// Get text content
	textContent := tg.Stats.String()

	// Get keyboard
	sendOptions, _ := tg.Template.Keyboard.Command.Statistics()

	// If a command, throw the message into the queue
	if interaction.IsCommand {
		// Wrap into a sendable
		sendable := sendables.Sendable{
			Type: sendables.Command,
			Message: &sendables.Message{
				TextContent: textContent,
				SendOptions: sendOptions,
			},
		}

		// Disable notification for channels
		sendable.Message.SendOptions.DisableNotification = isChannel(ctx.Chat())

		// Add to send queue as high-priority
		sendable.AddRecipient(chat, false)
		tg.Enqueue(&sendable, true)
		return nil
	}

	// Otherwise it's a callback request: update text, respond to callback
	tg.editCbMessage(ctx.Callback(), textContent, sendOptions)
	return tg.respondToCallback(ctx, "🔄 Refreshed stats", false)
}

// Handles the /settings command
func (tg *Bot) settingsHandler(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, true, "settings")

	if err != nil {
		log.Warn().Msg("Running settingsHandler failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// Load keyboard based on chat-type
	_, kb := tg.Template.Keyboard.Settings.Main(isGroup(ctx.Chat()))

	// Load message text content based on chat-type
	message := tg.Template.Messages.Settings.Main(isGroup(ctx.Chat()))
	message = utils.PrepareInputForMarkdown(message, "text")

	// Construct message
	msg := sendables.Message{
		TextContent: message,
		SendOptions: tb.SendOptions{
			ParseMode:           "MarkdownV2",
			ReplyMarkup:         &tb.ReplyMarkup{InlineKeyboard: kb},
			DisableNotification: isChannel(ctx.Chat()),
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type:    sendables.Command,
		Message: &msg,
	}

	sendable.AddRecipient(chat, false)

	// Add to send queue as high-priority
	tg.Enqueue(&sendable, true)

	return nil
}

// Handles requests to view a list of launch providers associated with a country code
func (tg *Bot) settingsCountryCodeView(ctx tb.Context) error {
	// Ensure callback data is valid
	data := strings.Split(ctx.Callback().Data, "/")

	if len(data) != 2 {
		log.Warn().Msgf("Got arbitrary data at cc/.. endpoint with length=%d", len(data))
		return nil
	}

	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, true, "settings")

	if err != nil {
		log.Warn().Msg("Running countryCodeListCallback failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// Get send-options
	sendOptions, _ := tg.Template.Keyboard.Settings.Subscription.ByCountryCode(chat, data[1])

	// Load message
	message := tg.Template.Messages.Settings.Subscription.ByCountryCode()
	message = utils.PrepareInputForMarkdown(message, "text")

	// Edit callback
	tg.editCbMessage(ctx.Callback(), message, sendOptions)

	// Respond to callback
	return tg.respondToCallback(ctx, fmt.Sprintf("Loaded %s", db.CountryCodeToName[data[1]]), false)
}

// Handles callbacks related to toggling notification settings
func (tg *Bot) notificationToggleCallback(ctx tb.Context) error {
	// Callback is of form (id, cc, all, time)/(id, cc, time-type, all-state)/(id-state, cc-state, time-state)
	data := strings.Split(ctx.Callback().Data, "/")

	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, true, "settings")

	if err != nil {
		log.Warn().Msg("Running notificationToggleCallback failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// Variable for updated keyboard following a callback
	var (
		cbText          string
		updatedKeyboard [][]tb.InlineButton
		showAlert       bool
	)

	switch data[0] {
	case "all":
		// Toggle all-flag
		toggleTo := utils.BinStringStateToBool[data[1]]
		chat.SetAllFlag(toggleTo)

		// Update keyboard
		_, updatedKeyboard = tg.Template.Keyboard.Settings.Subscription.Main(chat)

		// Callback response
		cbText = fmt.Sprintf("%s all notifications", utils.NotificationToggleCallbackString(toggleTo))
		showAlert = true

	case "id":
		// Toggle subscription for this ID
		toggleTo := utils.BinStringStateToBool[data[2]]
		chat.ToggleIdSubscription([]string{data[1]}, toggleTo)

		// Load updated keyboard
		intId, _ := strconv.Atoi(data[1])

		// Update keyboard
		_, updatedKeyboard = tg.Template.Keyboard.Settings.Subscription.ByCountryCode(chat, db.LSPShorthands[intId].Cc)

		// Callback response
		cbText = fmt.Sprintf("%s %s", utils.NotificationToggleCallbackString(toggleTo), db.LSPShorthands[intId].Name)

	case "cc":
		// Load all IDs associated with this country-code
		toggleTo := utils.BinStringStateToBool[data[2]]
		ids := db.AllIdsByCountryCode(data[1])

		// Toggle all IDs
		chat.ToggleIdSubscription(ids, toggleTo)

		// Update keyboard
		_, updatedKeyboard = tg.Template.Keyboard.Settings.Subscription.ByCountryCode(chat, data[1])

		// Callback response
		cbText = fmt.Sprintf("%s all for %s", utils.NotificationToggleCallbackString(toggleTo), db.CountryCodeToName[data[1]])
		showAlert = true

	case "time":
		if len(data) < 3 {
			log.Warn().Msgf("Insufficient data in time/ toggle endpoint: %d", len(data))
			return nil
		}

		// User is toggling a notification receive time
		toggleTo := utils.BinStringStateToBool[data[2]]
		chat.SetNotificationTimeFlag(data[1], toggleTo)

		// Update keyboard
		_, updatedKeyboard = tg.Template.Keyboard.Settings.Notifications(chat)

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
		_, updatedKeyboard = tg.Template.Keyboard.Settings.Group(chat)

		// Callback response
		cbText = fmt.Sprintf("%s permission status", utils.NotificationToggleCallbackString(toggleTo))
		showAlert = true

	default:
		log.Warn().Msgf("Received arbitrary data in notificationToggle: %s", ctx.Callback().Data)
		return errors.New("Received arbitrary data")
	}

	// Save user in a go-routine
	go tg.Db.SaveUser(chat)

	// Update the keyboard, as the state was modified
	sent, err := tg.Bot.EditReplyMarkup(ctx.Message(), &tb.ReplyMarkup{InlineKeyboard: updatedKeyboard})

	if err != nil {
		if !tg.handleError(nil, sent, err, ctx.Chat().ID) {
			return errors.New("Could not finish notification callback handling")
		}
	}

	// Respond to callback
	return tg.respondToCallback(ctx, cbText, showAlert)
}

// Handle launch mute/unmute callbacks
func (tg *Bot) muteCallback(ctx tb.Context) error {
	// Data is in the format id/toggleTo/notificationType
	data := strings.Split(ctx.Callback().Data, "/")

	migrationIdx := 0

	if len(data) > 3 {
		log.Warn().Msgf("Temporarily allowing old-format mute callback with mute/ prefix")
		migrationIdx = 1
	}

	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, true, "mute")

	if err != nil {
		log.Warn().Msg("Running muteCallback failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// Get bool state the mute status will be toggled to
	toggleTo := utils.BinStringStateToBool[data[1+migrationIdx]]

	// Toggle user's mute status (id, newState)
	success := chat.ToggleLaunchMute(data[0+migrationIdx], toggleTo)

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
			Data:   fmt.Sprintf("%s/%s/%s", data[0+migrationIdx], utils.ToggleBoolStateAsString[toggleTo], data[2+migrationIdx]),
		}

		// Set the existing mute button to the new one (always at zeroth index, regardless of expansion status)
		ctx.Message().ReplyMarkup.InlineKeyboard[0] = []tb.InlineButton{muteBtn}

		// Edit message's reply markup, since we don't need to touch the message content itself
		modified, err := tg.Bot.EditReplyMarkup(ctx.Message(), &tb.ReplyMarkup{InlineKeyboard: ctx.Message().ReplyMarkup.InlineKeyboard})

		if err != nil {
			// If not recoverable, return
			if !tg.handleError(nil, modified, err, ctx.Chat().ID) {
				return errors.New("Could not modify replyMarkup when handling a mute callback")
			}
		}

		if toggleTo {
			cbResponseText = "🔇 Launch muted!"
		} else {
			cbResponseText = "🔊 Launch unmuted! You will now receive notifications for this launch."
		}
	} else {
		cbResponseText = "⚠️ Request failed! This issue has been noted."
	}

	// Respond to callback
	return tg.respondToCallback(ctx, cbResponseText, true)
}

// Handler for settings callback requests. Returns a callback response and showAlert bool.
// TODO use the "Unique" property of inline buttons to do better callback handling
func (tg *Bot) settingsCallback(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, true, "settings")

	if err != nil {
		log.Warn().Msg("Running settingsCallback failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// Split data into an array
	cb := ctx.Callback()
	callbackData := strings.Split(cb.Data, "/")

	switch callbackData[0] {
	case "main": // User requested main settings menu
		// Load keyboard based on chat type
		sendOptions, _ := tg.Template.Keyboard.Settings.Main(isGroup(cb.Message.Chat))

		// Init text so we don't need to run it twice thorugh the markdown escaper
		message := tg.Template.Messages.Settings.Main(isGroup(cb.Message.Chat))
		message = utils.PrepareInputForMarkdown(message, "text")

		if len(callbackData) == 2 && callbackData[1] == "newMessage" {
			// Remove the keyboard button from the start message
			modified, err := tg.Bot.EditReplyMarkup(ctx.Message(), &tb.ReplyMarkup{})

			if err != nil {
				if !tg.handleError(nil, modified, err, ctx.Chat().ID) {
					return errors.New("Modifying settings.Main replyMarkup failed")
				}
			}

			// If a new message is requested, wrap into a sendable and send as new
			msg := sendables.Message{
				TextContent: message,
				SendOptions: sendOptions,
			}

			// Disable notification for channels
			msg.SendOptions.DisableNotification = isChannel(ctx.Chat())

			// Wrap into a sendable
			sendable := sendables.Sendable{
				Type:    sendables.Command,
				Message: &msg,
			}

			sendable.AddRecipient(chat, false)
			tg.Enqueue(&sendable, true)
		} else {
			tg.editCbMessage(cb, message, sendOptions)
		}

		return tg.respondToCallback(ctx, "⚙️ Loaded settings", false)

	case "tz":
		switch callbackData[1] {
		case "main":
			// Message text
			message := tg.Template.Messages.Settings.TimeZone.Main(chat.SavedTimeZoneInfo())

			// Load keyboard
			sendOptions, _ := tg.Template.Keyboard.Settings.TimeZone.Main()

			tg.editCbMessage(cb, message, sendOptions)
			return tg.respondToCallback(ctx, "🌍 Loaded time zone settings", false)

		case "begin":
			// Message text
			message := tg.Template.Messages.Settings.TimeZone.Setup()
			message = utils.PrepareInputForMarkdown(message, "text")

			// Load keyboard
			sendOptions, _ := tg.Template.Keyboard.Settings.TimeZone.Setup()

			// Edit message
			tg.editCbMessage(cb, message, sendOptions)
			return tg.respondToCallback(ctx, "🌍 Loaded time zone set-up", false)

		case "del":
			// Delete tz info, dump to disk
			chat.DeleteTimeZone()
			tg.Db.SaveUser(chat)

			// Message
			message := tg.Template.Messages.Settings.TimeZone.Deleted(chat.SavedTimeZoneInfo())
			message = utils.PrepareInputForMarkdown(message, "text")

			// Load keyboard
			sendOptions, _ := tg.Template.Keyboard.Settings.TimeZone.Deleted()

			tg.editCbMessage(cb, message, sendOptions)
			return tg.respondToCallback(ctx, "✅ Successfully deleted your time zone information!", true)
		}

	case "sub":
		// User requested subscription settings
		switch callbackData[1] {
		case "times":
			// Send-options with the keyboard
			sendOptions, _ := tg.Template.Keyboard.Settings.Notifications(chat)

			// Text
			message := tg.Template.Messages.Settings.Notifications()
			message = utils.PrepareInputForMarkdown(message, "text")

			tg.editCbMessage(cb, message, sendOptions)
			return tg.respondToCallback(ctx, "⏰ Loaded notification time settings", false)
		case "bycountry":
			// Dynamically generated notification preferences
			sendOptions, _ := tg.Template.Keyboard.Settings.Subscription.Main(chat)

			// Text for update
			message := tg.Template.Messages.Settings.Subscription.ByCountryCode()
			message = utils.PrepareInputForMarkdown(message, "text")

			tg.editCbMessage(cb, message, sendOptions)
			return tg.respondToCallback(ctx, "🔔 Notification settings loaded", false)
		}

	case "group":
		// Group-specific settings
		text := tg.Template.Messages.Settings.Group()
		text = utils.PrepareInputForMarkdown(text, "text")

		// Keyboard
		sendOptions, _ := tg.Template.Keyboard.Settings.Group(chat)

		// Capture the message ID of this setup message
		tg.editCbMessage(cb, text, sendOptions)
		return tg.respondToCallback(ctx, "👷 Loaded group settings", false)
	}

	return nil
}

// Handle notification message expansions
func (tg *Bot) expandMessageContent(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, false, "expandMessage")

	if err != nil {
		log.Warn().Msg("Running expandMessageContent failed")
		return nil
	}

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	// Split data field
	callbackData := strings.Split(ctx.Callback().Data, "/")

	var migrationIdx int
	if len(callbackData) > 2 {
		migrationIdx = 1
	}

	// Extract ID and notification type
	launchId := callbackData[0+migrationIdx]
	notification := callbackData[1+migrationIdx]

	// Find launch by ID (it may not exist in the cache anymore)
	launch, err := tg.Cache.FindLaunchById(launchId)

	if err != nil {
		cbRespStr := fmt.Sprintf("⚠️ %s", err.Error())
		return tg.respondToCallback(ctx, cbRespStr, true)
	}

	// Get text for this launch
	newText := launch.NotificationMessage(notification, true, tg.Username)
	newText = sendables.SetTime(newText, chat, launch.NETUnix, true, false, false)

	// Load mute status
	muted := chat.HasMutedLaunch(launch.Id)

	// Load keyboard
	sendOptions, _ := tg.Template.Keyboard.Command.Expand(launch.Id, notification, muted)

	// Edit message
	sent, err := tg.Bot.Edit(ctx.Callback().Message, newText, &sendOptions)

	if err != nil {
		// If not recoverable, return
		if !tg.handleError(nil, sent, err, ctx.Chat().ID) {
			return nil
		}
	}

	return tg.respondToCallback(ctx, "ℹ️ Notification expanded", false)
}

// Handles locations that the bot receives in a chat
func (tg *Bot) locationReplyHandler(ctx tb.Context) error {
	// Call senderIsAdmin separately, as it's an API call and may fail due to e.g. migration
	senderIsAdmin, err := tg.senderIsAdmin(ctx)

	if err != nil {
		log.Error().Err(err).Msg("Loading sender's admin status failed")
		return nil
	}

	// Verify sender is an admin
	if !senderIsAdmin {
		log.Debug().Msg("Location sender is not an admin")
		return nil
	}

	// If not a reply, return immediately
	if ctx.Message().ReplyTo == nil {
		log.Debug().Msg("Received a location, but it's not a reply")
		return nil
	}

	if isChannel(ctx.Chat()) || ctx.Message().ReplyTo.Sender.ID == tg.Bot.Me.ID {
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
		Data:   "main",
	}

	kb := [][]tb.InlineButton{{retBtn}}

	// Construct message
	msg := sendables.Message{
		TextContent: successText,
		SendOptions: tb.SendOptions{
			ParseMode:           "MarkdownV2",
			ReplyMarkup:         &tb.ReplyMarkup{InlineKeyboard: kb},
			DisableNotification: isChannel(ctx.Chat()),
		},
	}

	// Wrap into a sendable
	sendable := sendables.Sendable{
		Type:    sendables.Command,
		Message: &msg,
	}

	sendable.AddRecipient(chat, false)

	// Add to send queue as high-priority
	tg.Enqueue(&sendable, true)

	// Delete the setup message
	err = tg.Bot.Delete(tb.Editable(ctx.Message().ReplyTo))

	if err != nil {
		if !tg.handleError(ctx, nil, err, ctx.Chat().ID) {
			log.Warn().Msg("Deleting time zone setup message failed")
		}
	}

	return nil
}

// Test notification sends
func (tg *Bot) fauxNotification(ctx tb.Context) error {
	// Owner-only function
	if ctx.Message().Sender.ID != tg.Owner {
		log.Warn().Msgf("/send called by non-owner (%d)", ctx.Message().Sender.ID)
		return nil
	}

	// Load user from cache
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Message().Sender.ID), "tg")

	// Create message, get notification type
	testId := ctx.Data()

	if len(testId) == 0 {
		sendable := sendables.TextOnlySendable("No launch ID entered", chat)
		tg.Enqueue(sendable, true)
		return nil
	}

	launch, err := tg.Cache.FindLaunchById(testId)

	if err != nil {
		log.Error().Err(err).Msgf("Could not find launch by id=%s", testId)
		return nil
	}

	notifType := "1h"

	text := launch.NotificationMessage(notifType, false, tg.Username)
	kb := launch.TelegramNotificationKeyboard(notifType)

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

	// Create sendable
	sendable := sendables.Sendable{
		Platform:         "tg",
		Type:             sendables.Notification,
		NotificationType: notifType,
		LaunchId:         launch.Id,
		Message:          &msg,
	}

	// Flip to use actual recipients (here be dragons)
	useRealRecipients := true

	if useRealRecipients {
		sendable.Recipients = launch.NotificationRecipients(tg.Db, notifType, "tg")
	} else {
		sendable.AddRecipient(chat, false)
	}

	// Add to send queue as a normal notification
	tg.Enqueue(&sendable, false)

	return nil
}
