package telegram

import (
	"errors"
	"fmt"
	"launchbot/bots"
	"launchbot/bots/templates"
	"launchbot/db"
	"launchbot/sendables"
	"launchbot/stats"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

type Bot struct {
	Bot      *tb.Bot
	Db       *db.Database
	Cache    *db.Cache
	Queue    *bots.Queue
	Spam     *bots.Spam
	Stats    *stats.Statistics
	Template templates.Telegram
	Owner    int64
}

// Simple method to initialize the TelegramBot object
func (tg *Bot) Initialize(token string) {
	// Create queue for Telegram messages
	tg.Queue = &bots.Queue{
		Sendables:    make(map[string]*sendables.Sendable),
		HighPriority: &bots.HighPriorityQueue{HasItemsInQueue: false},
	}

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
	// TODO remove before production, or limit to only sending to the owner
	tg.Bot.Handle("/send", tg.fauxNotification)

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

// Responds to a callback with text, show alert if configured
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
		recoverable := handleTelegramError(ctx, err, tg)

		if recoverable {
			log.Error().Err(err).Msg("Recoverable error when responding to callback")
		}
	}

	return err
}

// Edit a message following a callback, and handle any errors
func (tg *Bot) editCbMessage(cb *tb.Callback, text string, sendOptions tb.SendOptions) *tb.Message {
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

// Attempt deleting the message associated with a context
func (tg *Bot) tryRemovingMessage(ctx tb.Context) error {
	// Get bot's member status
	bot, err := tg.Bot.ChatMemberOf(ctx.Chat(), tg.Bot.Me)

	if err != nil {
		log.Error().Msg("Loading bot's permissions in chat failed")
		handleTelegramError(ctx, err, tg)
		return err
	}

	if bot.CanDeleteMessages {
		// If we have permission to delete messages, delete the command message
		err = tg.Bot.Delete(ctx.Message())
	} else {
		// If You're not allowed to do that, return
		log.Debug().Msgf("Cannot delete messages in chat=%d", ctx.Chat().ID)
		return errors.New("Cannot delete message in chat")
	}

	// Check errors
	if err != nil {
		log.Error().Msg("Deleting message sent by a non-admin failed")
		handleTelegramError(ctx, err, tg)
		return errors.New("Deleting message sent by a non-admin failed")
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
func (tg *Bot) senderIsAdmin(ctx tb.Context) bool {
	// If not a group, return true
	if !isGroup(ctx.Chat().Type) {
		return true
	}

	// Load member
	member, err := tg.Bot.ChatMemberOf(ctx.Chat(), ctx.Sender())

	if err != nil {
		log.Error().Err(err).Msg("Getting ChatMemberOf() failed in isAdmin")
		return false
	}

	// Return true if user is admin or creator
	return member.Role == tb.Administrator || member.Role == tb.Creator
}

// Return true if chat is a group
// TODO determine whether this works in channels or not
func isGroup(chatType tb.ChatType) bool {
	return chatType == tb.ChatGroup || chatType == tb.ChatSuperGroup
}
