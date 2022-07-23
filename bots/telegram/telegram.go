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

	// Set-up command handlers
	tg.Bot.Handle("/start", tg.permissionedStart)
	tg.Bot.Handle("/next", tg.nextHandler)
	tg.Bot.Handle("/schedule", tg.scheduleHandler)
	tg.Bot.Handle("/statistics", tg.statsHandler)
	tg.Bot.Handle("/settings", tg.settingsHandler)
	tg.Bot.Handle("/feedback", tg.feedbackHandler)
	tg.Bot.Handle("/admin", tg.adminCommand)
	tg.Bot.Handle("/reply", tg.adminReply)

	// Handler for fake notification requests
	tg.Bot.Handle("/send", tg.fauxNotification)

	// Handle callbacks by button-type
	tg.Bot.Handle(&tb.InlineButton{Unique: "next"}, tg.nextHandler)
	tg.Bot.Handle(&tb.InlineButton{Unique: "schedule"}, tg.scheduleHandler)
	tg.Bot.Handle(&tb.InlineButton{Unique: "stats"}, tg.statsHandler)
	tg.Bot.Handle(&tb.InlineButton{Unique: "settings"}, tg.settingsCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "countryCodeView"}, tg.settingsCountryCodeView)
	tg.Bot.Handle(&tb.InlineButton{Unique: "notificationToggle"}, tg.notificationToggleCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "muteToggle"}, tg.muteCallback)
	tg.Bot.Handle(&tb.InlineButton{Unique: "expand"}, tg.expandMessageContent)
	tg.Bot.Handle(&tb.InlineButton{Unique: "admin"}, tg.adminCommand)

	// A generic callback handler to help with migrations/callback data changes
	tg.Bot.Handle(tb.OnCallback, tg.genericCallbackHandler)

	// Handle incoming locations for time zone setup messages
	tg.Bot.Handle(tb.OnLocation, tg.locationReplyHandler)

	// Catch service messages as they happen
	tg.Bot.Handle(tb.OnMigration, tg.migrationHandler)
	tg.Bot.Handle(tb.OnAddedToGroup, tg.unpermissionedStart)
	tg.Bot.Handle(tb.OnGroupCreated, tg.unpermissionedStart)
	tg.Bot.Handle(tb.OnSuperGroupCreated, tg.unpermissionedStart)
	tg.Bot.Handle(tb.OnChannelCreated, tg.unpermissionedStart)
	tg.Bot.Handle(tb.OnMyChatMember, tg.botMemberChangeHandler)
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
	senderIsAdmin := false

	// If chat is a group AND (command is admin-only OR users cannot send commands)
	if isGroup(ctx.Chat()) && (adminOnly || !chat.AnyoneCanSendCommands) {
		// Call senderIsAdmin separately, as it's an API call and may fail due to e.g. migration
		var err error
		senderIsAdmin, err = tg.senderIsAdmin(ctx)

		if err != nil {
			log.Error().Err(err).Msg("Loading sender's admin status failed")
			return nil, nil, err
		}
	}

	// Request is a command if the callback is nil
	isCommand := (ctx.Callback() == nil)

	// Take tokens depending on interaction type
	tokens := map[bool]int{true: 2, false: 1}[isCommand]

	// Build interaction for spam handling
	interaction := bots.Interaction{
		IsAdminOnly:   adminOnly,
		IsCommand:     isCommand,
		IsGroup:       isGroup(ctx.Chat()),
		CallerIsAdmin: senderIsAdmin,
		Name:          name,
		Tokens:        tokens,
	}

	// Allow any user to expand notification messages
	if name == "expandMessage" {
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

	if bot.CanDeleteMessages || !isGroup(ctx.Chat()) {
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
	// If not a group, return true
	if !isGroup(ctx.Chat()) {
		return true, nil
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

// Return true if chat is a group
// TODO determine whether this works in channels or not
func isGroup(chat *tb.Chat) bool {
	return (chat.Type == tb.ChatGroup) || (chat.Type == tb.ChatSuperGroup)
}

// Is the sender the owner of the bot?
func (tg *Bot) senderIsOwner(ctx tb.Context) bool {
	if ctx.Callback() == nil {
		return ctx.Message().Sender.ID == tg.Owner
	}

	return ctx.Callback().Sender.ID == tg.Owner
}
