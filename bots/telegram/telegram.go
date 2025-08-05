package telegram

import (
	"fmt"
	"launchbot/bots"
	"launchbot/bots/templates"
	"launchbot/db"
	"launchbot/sendables"
	"launchbot/stats"
	"launchbot/users"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

type Bot struct {
	Bot               *tb.Bot
	Db                *db.Database
	Cache             *db.Cache
	NotificationQueue chan *sendables.Sendable
	CommandQueue      chan *sendables.Sendable
	Quit              Quit
	Spam              *bots.Spam
	Stats             *stats.Statistics
	Template          templates.Telegram
	Username          string
	Owner             int64
}

// Quit is used to manage a graceful shutdown flow
type Quit struct {
	Channel       chan int
	Started       bool
	Finalized     bool
	ExitedWorkers int
	WaitGroup     *sync.WaitGroup
	Mutex         sync.Mutex
}

// A valid command for the bot and associated named interactions (interaction.name)
type Command string

const (
	Next       Command = "next"
	Schedule   Command = "schedule"
	Statistics Command = "statistics"
	Settings   Command = "settings"
	Feedback   Command = "feedback"
)

// Command descriptions, in case we need to manually register them
var commandDescriptions = map[Command]string{
	Next:       "🚀 Upcoming launches",
	Schedule:   "📆 Launch schedule",
	Statistics: "📊 LaunchBot statistics",
	Settings:   "🔔 Notification settings",
	Feedback:   "✍️ Send feedback to developer",
}

// A list of all registered (public) commands: we may still handle non-public commands.
var Commands = [5]Command{Next, Schedule, Statistics, Settings, Feedback}

// Simple method to initialize the TelegramBot object
func (tg *Bot) Initialize(token string) {
	// Init keyboard-holder struct
	tg.Template = templates.Telegram{}
	tg.Template.Init()

	var err error

	tg.Bot, err = tb.NewBot(tb.Settings{
		Token:  token,
		Poller: &tb.LongPoller{Timeout: time.Second * 60},
		Client: &http.Client{Timeout: time.Second * 60},
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Error starting Telegram bot")
	}

	// Set bot's username
	tg.Username = tg.Bot.Me.Username

	// Ensure all commands are registered
	tg.ensureCommands()

	// Set-up command handlers
	tg.Bot.Handle("/start", tg.permissionedStart)
	tg.Bot.Handle("/next", tg.nextHandler)
	tg.Bot.Handle("/schedule", tg.scheduleHandler)
	tg.Bot.Handle("/statistics", tg.statsHandler)
	tg.Bot.Handle("/settings", tg.settingsHandler)
	tg.Bot.Handle("/feedback", tg.feedbackHandler)
	tg.Bot.Handle("/admin", tg.adminCommand)
	tg.Bot.Handle("/reply", tg.adminReply)
	tg.Bot.Handle("/broadcast", tg.broadcastHandler)

	// Handler for fake notification requests
	tg.Bot.Handle("/send", tg.fauxNotification)

	// Handle callbacks by button-type
	tg.Bot.Handle(&tb.InlineButton{Unique: "next"}, tg.wrapCallbackHandler(tg.nextHandler))
	tg.Bot.Handle(&tb.InlineButton{Unique: "schedule"}, tg.wrapCallbackHandler(tg.scheduleHandler))
	tg.Bot.Handle(&tb.InlineButton{Unique: "stats"}, tg.wrapCallbackHandler(tg.statsHandler))
	tg.Bot.Handle(&tb.InlineButton{Unique: "settings"}, tg.wrapCallbackHandler(tg.settingsCallback))
	tg.Bot.Handle(&tb.InlineButton{Unique: "countryCodeView"}, tg.wrapCallbackHandler(tg.settingsCountryCodeView))
	tg.Bot.Handle(&tb.InlineButton{Unique: "notificationToggle"}, tg.wrapCallbackHandler(tg.notificationToggleCallback))
	tg.Bot.Handle(&tb.InlineButton{Unique: "muteToggle"}, tg.wrapCallbackHandler(tg.muteCallback))
	tg.Bot.Handle(&tb.InlineButton{Unique: "keywords"}, tg.wrapCallbackHandler(tg.keywordsCallback))
	tg.Bot.Handle(&tb.InlineButton{Unique: "expand"}, tg.wrapCallbackHandler(tg.expandMessageContent))
	tg.Bot.Handle(&tb.InlineButton{Unique: "admin"}, tg.wrapCallbackHandler(tg.adminCommand))

	// A generic, catch-all callback handler to help with migrations/deprecations
	tg.Bot.Handle(tb.OnCallback, tg.wrapCallbackHandler(tg.genericCallbackHandler))

	// Handle incoming locations for time-zone setup messages
	tg.Bot.Handle(tb.OnLocation, tg.locationReplyHandler)

	// Handle text messages for keyword input
	tg.Bot.Handle(tb.OnText, tg.textMessageHandler)

	// Catch service messages as they happen
	tg.Bot.Handle(tb.OnMigration, tg.migrationHandler)
	tg.Bot.Handle(tb.OnAddedToGroup, tg.unpermissionedStart)
	tg.Bot.Handle(tb.OnGroupCreated, tg.unpermissionedStart)
	tg.Bot.Handle(tb.OnSuperGroupCreated, tg.unpermissionedStart)
	tg.Bot.Handle(tb.OnMyChatMember, tg.botMemberChangeHandler)

	// Handle Telegram channel related events
	tg.Bot.Handle(tb.OnChannelCreated, tg.unpermissionedStart)
	tg.Bot.Handle(tb.OnChannelPost, tg.channelProcessor)
}

// Ensures all required commands are registered properly
func (tg *Bot) ensureCommands() {
	// Load available commands
	registeredCommands, err := tg.Bot.Commands()

	if err != nil {
		log.Fatal().Err(err).Msg("Loading available commands failed")
	}

	// If zero commands are registed, do it manually
	if len(registeredCommands) == 0 {
		tgCommands := []tb.Command{}

		for _, cmd := range Commands {
			// Load the description
			cmdDescription, ok := commandDescriptions[cmd]

			if !ok {
				log.Fatal().Msgf("Could not load description for command=%s", string(cmd))
			}

			// Add to the command list
			tgCommands = append(tgCommands, tb.Command{
				Text:        string(cmd),
				Description: cmdDescription,
			})
		}

		// Set the commands
		err := tg.Bot.SetCommands(tgCommands)

		if err != nil {
			log.Error().Err(err).Msg("Setting commands for the bot failed")
		} else {
			log.Debug().Msgf("Commands set for the bot successfully!")
		}

		return
	}

	foundCommandCount := 0

	// Parse available commands, assign to bot
	for _, registeredCommand := range registeredCommands {
		for _, expectedCommand := range Commands {
			if registeredCommand.Text == string(expectedCommand) {
				foundCommandCount++
				// TODO register commands not found (do a map[Command]bool (found/not))
			}
		}
	}

	if foundCommandCount != len(Commands) {
		log.Warn().Msgf("Expected to find %d commands, but only %d are registered",
			len(Commands), foundCommandCount)
	}
}

// Channels do not support "native" bot commands, thus we need to do some manual
// processing to get commands we are interested in.
func (tg *Bot) channelProcessor(ctx tb.Context) error {
	// Extract the message pointer from the context
	msg := ctx.Message()

	// If the message is a location, check if it is a reply to the bot
	if msg.Location != nil {
		// A location and a command cannot coexist, so check if it's valid -> return
		return tg.locationReplyHandler(ctx)
	}

	// Pre-init variables for the possible valid command we may find
	var foundValidCommand Command
	var validCommandFound bool

	// See if any of the entitites contain a bot command
	for _, entity := range msg.Entities {
		if entity.Type == tb.EntityCommand {
			// Process command: remove the slash-prefix, split by a (possible) @-symbol
			cmdSplit := strings.Split(msg.EntityText(entity)[1:], "@")

			if len(cmdSplit) > 1 {
				// If the length is greater than one (ie. it contains an '@'), check that the command is for us
				if cmdSplit[1] != tg.Username {
					return nil
				}
			}

			// Check that the command is in the list of known valid commands
			for _, validCommand := range Commands {
				if strings.ToLower(cmdSplit[0]) == string(validCommand) {
					foundValidCommand = validCommand
					validCommandFound = true
					break
				}
			}
		}

		if validCommandFound {
			break
		}
	}

	if !validCommandFound {
		// If no (valid) command found, return
		return nil
	}

	// Get bot's member status
	bot, err := tg.Bot.ChatMemberOf(ctx.Chat(), tg.Bot.Me)

	if err != nil {
		log.Error().Msg("Loading bot's permissions in channel failed")
		tg.handleError(ctx, nil, err, ctx.Chat().ID)
		return nil
	}

	// Check if we can post in this channel
	if !bot.CanPostMessages {
		log.Debug().Msgf("Found a valid command (%s) in channel=%d, but we cannot post there",
			foundValidCommand, ctx.Chat().ID)
		return nil
	}

	// We found a valid command, and we can post in the channel: switch-case the command
	switch foundValidCommand {
	case Next:
		_ = tg.nextHandler(ctx)
	case Schedule:
		_ = tg.scheduleHandler(ctx)
	case Statistics:
		_ = tg.statsHandler(ctx)
	case Settings:
		_ = tg.settingsHandler(ctx)
	case Feedback:
		_ = tg.feedbackHandler(ctx)
	default:
		log.Error().Msgf(
			"Tried to switch-case found valid command in chan-processor, but defaulted (cmd=%s)",
			string(foundValidCommand))
	}

	return nil
}

// A generic callback handler, to notify users of the bot having been migrated/upgraded.
// Effectively, if a callback fails to be properly routed to any existing callback endpoint,
// the callback _has_ to be using an old format, which we cannot respond to.
func (tg *Bot) genericCallbackHandler(ctx tb.Context) error {
	// Load chat and generate the interaction
	chat, interaction, err := tg.buildInteraction(ctx, false, "generic")

	if err != nil {
		log.Warn().Msg("Running generic callback handler failed")
		return nil
	}

	// Extract data for logging purposes
	cbData := ctx.Callback().Data

	// Run permission and spam management
	if !tg.Spam.PreHandler(interaction, chat, tg.Stats) {
		log.Debug().Msgf("User in chat=%s attempted to use a generic callback, ignoring (data=%s)",
			chat.Id, cbData)

		return tg.interactionNotAllowed(ctx, interaction.IsCommand)
	}

	log.Debug().Msgf(
		"Chat=%s attempted to use a generic callback, responding with migration warning (data=%s)",
		chat.Id, cbData)

	return tg.respondToCallback(ctx, tg.Template.Messages.Migrated(), true)
}

// wrapCallbackHandler wraps a callback handler with panic recovery
func (tg *Bot) wrapCallbackHandler(handler func(tb.Context) error) func(tb.Context) error {
	return func(ctx tb.Context) error {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Msgf("Panic in callback handler: %v", r)
				// Try to respond to the callback to acknowledge it
				if ctx != nil && ctx.Callback() != nil {
					_ = tg.respondToCallback(ctx, "⚠️ An error occurred. Please try again.", false)
				}
			}
		}()
		return handler(ctx)
	}
}

// Responds to a callback with text, show alert if configured. Always returns a nil.
func (tg *Bot) respondToCallback(ctx tb.Context, text string, showAlert bool) error {
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
		tg.handleError(ctx, nil, err, ctx.Chat().ID)
	}

	// Despite the error, we always return a nil due to how Telebot handlers errors.
	return nil
}

// Edit a message following a callback, and handle any errors
func (tg *Bot) editCbMessage(cb *tb.Callback, text string, sendOptions tb.SendOptions) *tb.Message {
	// Edit message
	msg, err := tg.Bot.Edit(cb.Message, text, &sendOptions)

	if err != nil {
		tg.handleError(nil, msg, err, cb.Message.Chat.ID)
		return nil
	}

	return msg
}

// Builds a spam- and rate-limit managed interaction from context and interaction-specific limits.
func (tg *Bot) buildInteraction(ctx tb.Context, adminOnly bool, name string) (*users.User, *bots.Interaction, error) {
	// Load chat
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	if chat.Type == "" {
		tg.loadChatType(chat)
	}

	// Request is a command if the callback is nil
	isCommand := (ctx.Callback() == nil)
	senderIsAdmin := false

	// If (is group OR is a callback in a channel) AND (command is admin-only OR users cannot send commands)
	if (isGroup(ctx.Chat()) || (isChannel(ctx.Chat()) && !isCommand)) && (adminOnly || !chat.AnyoneCanSendCommands) {
		var err error
		senderIsAdmin, err = tg.senderIsAdmin(ctx)

		if err != nil {
			log.Error().Err(err).Msg("Loading sender's admin status failed")
			return nil, nil, err
		}
	} else if isChannel(ctx.Chat()) && isCommand {
		// Special edge case for channel posts: in this case, sender is always an admin
		senderIsAdmin = true
	}

	// Take tokens depending on interaction type
	tokens := map[bool]int{true: 2, false: 1}[isCommand]

	// Build interaction for spam handling
	interaction := bots.Interaction{
		IsAdminOnly:    adminOnly,
		IsCommand:      isCommand,
		IsPermissioned: !isPrivate(ctx.Chat()),
		CallerIsAdmin:  senderIsAdmin,
		Name:           name,
		Tokens:         tokens,
	}

	// Allow any user to expand notification messages in groups and channels
	if name == "expandMessage" && !isPrivate(ctx.Chat()) {
		interaction.AnyoneCanUse = true
	}

	if !isCommand {
		// If a callback, add callback data to the interaction
		interaction.CbData = ctx.Callback().Data
	}

	return chat, &interaction, nil
}

// Attempt deleting the message associated with a context
func (tg *Bot) tryRemovingMessage(ctx tb.Context) error {
	// Get bot's member status
	bot, err := tg.Bot.ChatMemberOf(ctx.Chat(), tg.Bot.Me)

	if err != nil {
		log.Error().Msg("Loading bot's permissions in chat failed")
		tg.handleError(ctx, nil, err, ctx.Chat().ID)
		return nil
	}

	if bot.CanDeleteMessages || isPrivate(ctx.Chat()) {
		// If we have permission to delete messages, delete the command message
		err = tg.Bot.Delete(ctx.Message())
	} else {
		// If the bot is not allowed to delete messages, return
		log.Error().Err(err).Msgf("Cannot delete messages in chat=%d", ctx.Chat().ID)
		return nil
	}

	// Check errors
	if err != nil {
		log.Debug().Msg("Deleting message sent by a non-admin failed")
		tg.handleError(ctx, nil, err, ctx.Chat().ID)
		return nil
	}

	log.Debug().Msgf("Deleted message by non-admin in chat=%d", ctx.Chat().ID)
	return nil
}

// When an interaction was not allowed, handle appropriately
func (tg *Bot) interactionNotAllowed(ctx tb.Context, isCommand bool) error {
	if isCommand {
		// If a command, try removing the message
		return tg.tryRemovingMessage(ctx)
	}

	// Otherwise, respond with a callback
	return tg.respondToCallback(ctx, tg.Template.Messages.Service.InteractionNotAllowed(), true)
}

// Handles migration service messages
func (tg *Bot) migrationHandler(ctx tb.Context) error {
	from, to := ctx.Migration()
	log.Info().Msgf("Chat upgraded to a supergroup: migrating chat from %d to %d...", from, to)

	tg.Db.MigrateGroup(from, to, "tg")
	return nil
}

// Handles changes related to the bot's member status in a chat
func (tg *Bot) botMemberChangeHandler(ctx tb.Context) error {
	// Chat associated with this update
	chat := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")

	// If we were kicked or somehow managed to leave the chat, remove the chat from the db
	if ctx.ChatMember().NewChatMember.Role == tb.Kicked || ctx.ChatMember().NewChatMember.Role == tb.Left {
		log.Info().Msgf("Kicked or left from chat=%s, deleting from database...", chat.Id)
		tg.Db.RemoveUser(chat)
	}

	return nil
}

// Return a chat user's admin status
// FUTURE: cache, and keep track of member status changes as they happen
func (tg *Bot) senderIsAdmin(ctx tb.Context) (bool, error) {
	// If a private chat, return early
	if isPrivate(ctx.Chat()) {
		return true, nil
	}

	// Ensure chat and sender are present
	if ctx.Chat() == nil || ctx.Sender() == nil {
		log.Error().Msgf("[senderIsAdmin] Attempted loading ChatMemberOf, but chat or sender is not present")
		log.Error().Msgf("Context: %+v", ctx)
		return false, fmt.Errorf("Chat or sender not present")
	}

	// Load member
	member, err := tg.Bot.ChatMemberOf(ctx.Chat(), ctx.Sender())

	if err != nil {
		log.Error().Err(err).Msg("Getting ChatMemberOf() failed in senderIsAdmin")
		tg.handleError(ctx, nil, err, ctx.Chat().ID)

		return false, err
	}

	// Return true if user is admin or creator
	return member.Role == tb.Administrator || member.Role == tb.Creator, nil
}

// Loads a Telegram chat object from a user ID
func (tg *Bot) LoadChatFromUser(user *users.User) *tb.Chat {
	id, err := strconv.ParseInt(user.Id, 10, 64)

	if err != nil {
		log.Error().Err(err).Msg("Converting str user ID to int64 failed")
		return nil
	}

	// Load chat
	chat, err := tg.Bot.ChatByID(id)

	if err != nil {
		// Check if this is a "chat not found" error
		if strings.Contains(err.Error(), "chat not found") {
			log.Warn().Err(err).Msgf("Chat not found when loading user=%s - chat may have been deleted", user.Id)
		} else {
			log.Error().Err(err).Msgf("Loading user=%s failed", user.Id)
		}
		return nil
	}

	return chat
}

// Map Telegram chat type to user chat type
func TelegramChatToUserType(chat *tb.Chat) users.ChatType {
	if isGroup(chat) {
		return users.Group
	} else if isChannel(chat) {
		return users.Channel
	} else {
		return users.Private
	}
}

// Return true if chat is a group
func isGroup(chat *tb.Chat) bool {
	return (chat.Type == tb.ChatGroup) || (chat.Type == tb.ChatSuperGroup)
}

// Return true if chat is a channel
func isChannel(chat *tb.Chat) bool {
	return (chat.Type == tb.ChatChannel) || (chat.Type == tb.ChatChannelPrivate)
}

// Returns true if chat is a private chat
func isPrivate(chat *tb.Chat) bool {
	return chat.Type == tb.ChatPrivate
}

// Is the sender the owner of the bot?
func (tg *Bot) senderIsOwner(ctx tb.Context) bool {
	if ctx.Callback() == nil {
		return ctx.Message().Sender.ID == tg.Owner
	}

	return ctx.Callback().Sender.ID == tg.Owner
}

// Loads and sets the Telebot chat type for a Launchbot user
func (tg *Bot) loadChatType(user *users.User) {
	// Load Telebot chat object
	tbChat := tg.LoadChatFromUser(user)

	if tbChat != nil {
		user.Type = TelegramChatToUserType(tbChat)
		log.Debug().Msgf("Loaded user-type=%s for chat=%s", user.Type, user.Id)
	} else {
		log.Debug().Msgf("Failed to load chat type for user=%s - continuing without type", user.Id)
	}
}
