package bots

import (
	"fmt"
	"launchbot/sendables"
	"launchbot/users"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

/*
// General errors
var (
	ErrTooLarge     = NewError(400, "Request Entity Too Large")
	ErrUnauthorized = NewError(401, "Unauthorized")
	ErrNotFound     = NewError(404, "Not Found")
	ErrInternal     = NewError(500, "Internal Server Error")
)

// Bad request errors
var (
	ErrBadButtonData          = NewError(400, "Bad Request: BUTTON_DATA_INVALID")
	ErrBadPollOptions         = NewError(400, "Bad Request: expected an Array of String as options")
	ErrBadURLContent          = NewError(400, "Bad Request: failed to get HTTP URL content")
	ErrCantEditMessage        = NewError(400, "Bad Request: message can't be edited")
	ErrCantRemoveOwner        = NewError(400, "Bad Request: can't remove chat owner")
	ErrCantUploadFile         = NewError(400, "Bad Request: can't upload file by URL")
	ErrCantUseMediaInAlbum    = NewError(400, "Bad Request: can't use the media of the specified type in the album")
	ErrChatAboutNotModified   = NewError(400, "Bad Request: chat description is not modified")
	ErrChatNotFound           = NewError(400, "Bad Request: chat not found")
	ErrEmptyChatID            = NewError(400, "Bad Request: chat_id is empty")
	ErrEmptyMessage           = NewError(400, "Bad Request: message must be non-empty")
	ErrEmptyText              = NewError(400, "Bad Request: text is empty")
	ErrFailedImageProcess     = NewError(400, "Bad Request: IMAGE_PROCESS_FAILED", "Image process failed")
	ErrGroupMigrated          = NewError(400, "Bad Request: group chat was upgraded to a supergroup chat")
	ErrMessageNotModified     = NewError(400, "Bad Request: message is not modified")
	ErrNoRightsToDelete       = NewError(400, "Bad Request: message can't be deleted")
	ErrNoRightsToRestrict     = NewError(400, "Bad Request: not enough rights to restrict/unrestrict chat member")
	ErrNoRightsToSend         = NewError(400, "Bad Request: have no rights to send a message")
	ErrNoRightsToSendGifs     = NewError(400, "Bad Request: CHAT_SEND_GIFS_FORBIDDEN", "sending GIFS is not allowed in this chat")
	ErrNoRightsToSendPhoto    = NewError(400, "Bad Request: not enough rights to send photos to the chat")
	ErrNoRightsToSendStickers = NewError(400, "Bad Request: not enough rights to send stickers to the chat")
	ErrNotFoundToDelete       = NewError(400, "Bad Request: message to delete not found")
	ErrNotFoundToForward      = NewError(400, "Bad Request: message to forward not found")
	ErrNotFoundToReply        = NewError(400, "Bad Request: reply message not found")
	ErrQueryTooOld            = NewError(400, "Bad Request: query is too old and response timeout expired or query ID is invalid")
	ErrSameMessageContent     = NewError(400, "Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message")
	ErrStickerEmojisInvalid   = NewError(400, "Bad Request: invalid sticker emojis")
	ErrStickerSetInvalid      = NewError(400, "Bad Request: STICKERSET_INVALID", "Stickerset is invalid")
	ErrStickerSetInvalidName  = NewError(400, "Bad Request: invalid sticker set name is specified")
	ErrStickerSetNameOccupied = NewError(400, "Bad Request: sticker set name is already occupied")
	ErrTooLongMarkup          = NewError(400, "Bad Request: reply markup is too long")
	ErrTooLongMessage         = NewError(400, "Bad Request: message is too long")
	ErrUserIsAdmin            = NewError(400, "Bad Request: user is an administrator of the chat")
	ErrWrongFileID            = NewError(400, "Bad Request: wrong file identifier/HTTP URL specified")
	ErrWrongFileIDCharacter   = NewError(400, "Bad Request: wrong remote file id specified: Wrong character in the string")
	ErrWrongFileIDLength      = NewError(400, "Bad Request: wrong remote file id specified: Wrong string length")
	ErrWrongFileIDPadding     = NewError(400, "Bad Request: wrong remote file id specified: Wrong padding in the string")
	ErrWrongFileIDSymbol      = NewError(400, "Bad Request: wrong remote file id specified: can't unserialize it. Wrong last symbol")
	ErrWrongTypeOfContent     = NewError(400, "Bad Request: wrong type of the web page content")
	ErrWrongURL               = NewError(400, "Bad Request: wrong HTTP URL specified")
	ErrForwardMessage         = NewError(400, "Bad Request: administrators of the chat restricted message forwarding")
)

// Forbidden errors
var (
	ErrBlockedByUser        = NewError(403, "Forbidden: bot was blocked by the user")
	ErrKickedFromGroup      = NewError(403, "Forbidden: bot was kicked from the group chat")
	ErrKickedFromSuperGroup = NewError(403, "Forbidden: bot was kicked from the supergroup chat")
	ErrNotStartedByUser     = NewError(403, "Forbidden: bot can't initiate conversation with a user")
	ErrUserIsDeactivated    = NewError(403, "Forbidden: user is deactivated")
)

https://github.com/go-telebot/telebot/blob/v3.0.0/errors.go#L33
*/

func errorMonitor(err error, tg *TelegramBot) {
	// Create a simple error message
	errMsg := fmt.Sprintf("Processing failure: %#v", err.Error())
	msg := sendables.Message{TextContent: &errMsg, SendOptions: tb.SendOptions{}}
	user := tg.Cache.FindUser(fmt.Sprintf("%d", tg.Owner), "tg")
	recipients := users.SingleUserList(user, false, "tg")

	// Wrap in a sendable
	sendable := sendables.Sendable{
		Message: &msg, Recipients: recipients,
	}

	// Enqueue message as high-priority
	tg.Queue.Enqueue(&sendable, tg, true)
}

/* Wrapper for warning of unhandled errors */
func warnUnhandled(err error) {
	log.Error().Err(err).Msg("Unhandled Telegram error")
}

/* Telegram error handler

Returns:
- bool: indicates if the error is recoverable, as in if the previous execution
				can still proceed after the error.
*/

// Handle errors associated with outgoing data, such as sends and edits
func handleSendError(sent *tb.Message, err error, tg *TelegramBot) bool {
	return true
}

// Handle errors associated with incoming requests
func handleTelegramError(ctx tb.Context, err error, tg *TelegramBot) bool {
	switch err.Error() {
	case "":
		log.Warn().Msg("handleTelegramError called with nil error")
		return true

	// General errors (400, 401, 404, 500) [all handled]
	case tb.ErrTooLarge.Error():
		warnUnhandled(err)
	case tb.ErrUnauthorized.Error():
		warnUnhandled(err)
	case tb.ErrNotFound.Error():
		warnUnhandled(err)
	case tb.ErrInternal.Error():
		warnUnhandled(err)

	/* 400 (bad request)

	TODO: handle non-send related errors */
	case tb.ErrMessageNotModified.Error():
		return true

	case tb.ErrSameMessageContent.Error():
		return true

	case tb.ErrChatNotFound.Error():
		warnUnhandled(err)

	case tb.ErrEmptyChatID.Error():
		log.Trace().Err(err).Msg("Empty chat ID in message")

	case tb.ErrEmptyMessage.Error():
		log.Trace().Err(err).Msg("Empty message")

	case tb.ErrEmptyText.Error():
		log.Trace().Err(err).Msg("Empty text in message")

	case tb.ErrGroupMigrated.Error():
		log.Error().Err(err).Msg("Group migrated")

	case tb.ErrNoRightsToSend.Error():
		log.Trace().Err(err).Msg("No rights to send message to chat")

	case tb.ErrTooLongMarkup.Error():
		log.Trace().Err(err).Msg("Markup is too long")

	case tb.ErrTooLongMessage.Error():
		log.Trace().Err(err).Msg("Message is too long")

	case tb.ErrWrongURL.Error():
		log.Trace().Err(err).Msg("Wrong URL")

	// Error 403 (forbidden) [all handled]
	case tb.ErrBlockedByUser.Error():
		user := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")
		log.Debug().Msgf("Bot was blocked by user=%s, removing from database...", user.Id)
		tg.Db.RemoveUser(user)

	case tb.ErrKickedFromGroup.Error():
		user := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")
		log.Debug().Msgf("Bot was kicked from group=%s, removing from database...", user.Id)
		tg.Db.RemoveUser(user)

	case tb.ErrKickedFromSuperGroup.Error():
		user := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")
		log.Debug().Msgf("Bot was kicked from supergroup=%s, removing from database...", user.Id)
		tg.Db.RemoveUser(user)

	case tb.ErrNotStartedByUser.Error():
		user := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")
		log.Debug().Msgf("Bot was never started by user=%s, removing from database...", user.Id)
		tg.Db.RemoveUser(user)

	case tb.ErrUserIsDeactivated.Error():
		user := tg.Cache.FindUser(fmt.Sprint(ctx.Chat().ID), "tg")
		log.Debug().Msgf("User=%s has been deactivated, removing from database...", user.Id)
		tg.Db.RemoveUser(user)

	// If the error is not in the cases, default to unhandled
	default:
		log.Trace().Err(err).Msg("Error case fell through (defaulted)")
		warnUnhandled(err)
		errorMonitor(err, tg)
	}

	return false
}
