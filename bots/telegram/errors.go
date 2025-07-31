package telegram

import (
	"errors"
	"fmt"
	"launchbot/sendables"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

/*
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
	ErrNotFoundToEdit         = NewError(400, "Bad Request: message to edit not found")
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

https://github.com/go-telebot/telebot/blob/v3.0.0/errors.go#L33
*/

// On unhandled errors, send a notification to the administrator
func notifyAdminOfError(tg *Bot, err error, wasGeneric bool) {
	// Create a simple error message
	var errMsg string
	if wasGeneric {
		errMsg = fmt.Sprintf("Unhandled error: %#v", err.Error())
	} else {
		errMsg = fmt.Sprintf("Unhandled Telegram error: %#v", err.Error())
	}

	// Wrap in a sendable
	sendable := sendables.Sendable{
		Platform:       "tg",
		IsHighPriority: true,
		Type:           sendables.Command,
		Message: &sendables.Message{
			TextContent: errMsg, SendOptions: tb.SendOptions{},
		},
	}

	// Add owner as recipient
	sendable.AddRecipient(tg.Cache.FindUser(fmt.Sprintf("%d", tg.Owner), "tg"), false)

	// Enqueue message as high-priority
	tg.Enqueue(&sendable, true)
}

// Wrapper for warning of unhandled errors */
func warnUnhandled(err error, wasGeneric bool) {
	if wasGeneric {
		log.Error().Err(err).Msgf("Unhandled error in handleGenericError")
	} else {
		log.Error().Err(err).Msg("Unhandled Telegram error")
	}

	log.Debug().Msgf("Unhandled error: %+v", err)
}

// A generic error handler
func handleGenericError(err error) bool {
	warnUnhandled(err, true)
	return false
}

func (tg *Bot) handleError(ctx tb.Context, sent *tb.Message, err error, id int64) bool {
	// We may unintentionally call error handler with a nil error
	if err == nil {
		log.Warn().Msg("handleGenericError called with a nil error")
		return true
	}

	// Load user
	chat := tg.Cache.FindUser(fmt.Sprint(id), "tg")

	// Context might be nil: if it is, then this is a send-related error
	// Send-related errors are handled differently, mainly during migrations
	isSendError := ctx == nil

	// Two special error cases: flood-errors and group-errors
	var floodErr tb.FloodError
	var groupErr tb.GroupError

	// Check if error is a rate-limit
	if errors.As(err, &floodErr) {
		log.Warn().Err(err).Msgf("Received a tb.FloodError (retryAfter=%d): sleeping for 5 seconds...", floodErr.RetryAfter)
		time.Sleep(time.Duration(5) * time.Second)
		return true
	}

	// Check if error is a group-error
	if errors.As(err, &groupErr) {
		// TODO implement a FindMigratedUser -> send new message?
		log.Warn().Err(err).Msgf("tb.ErrGroupMigrated: running migration (isSendError=%v, groupErr=%#v)", isSendError, groupErr)
		migratedTo := groupErr.MigratedTo

		if isSendError {
			// If called with a send-error, log the message
			log.Warn().Msgf("Migration encountered with a send error, sent=%#v, groupErr=%#v", sent, groupErr)

			if sent != nil {
				log.Warn().Msgf("Migration properties of sendable: %d ➙ %d", id, migratedTo)
				tg.Db.MigrateGroup(id, migratedTo, "tg")
			}
		} else {
			// When we have a context, re-send the message
			log.Info().Msgf("Migration properties of context: %d ➙ %d", id, migratedTo)
			tg.Db.MigrateGroup(id, migratedTo, "tg")

			// Swap chat IDs
			newChat := tb.ChatID(migratedTo)

			// Re-send the original message associated with this callback
			originalMessage := ctx.Callback().Message

			// Re-send the message to the new chat ID
			sent, err := tg.Bot.Send(newChat, originalMessage.Text, originalMessage.ReplyMarkup)

			if err != nil {
				log.Error().Err(err).Msg("Attempting to re-send migrated message yielded an error")
				tg.handleError(nil, sent, err, migratedTo)
			} else {
				log.Info().Msg("Successfully sent a message to the migrated chat")
			}
		}

		notifyAdminOfError(tg, err, false)
		return false
	}

	// Custom errors that are not present in Telebot, for whatever reason
	tbGroupDeleterErr := tb.NewError(403, "telegram: Forbidden: the group chat was deleted")
	tbGroupDeleterErr2 := tb.NewError(403, "Forbidden: the group chat was deleted")

	// Check for specific error messages in string form
	if err != nil && strings.Contains(err.Error(), "message to edit not found") {
		log.Warn().Msg("Message not found to edit - it may have been deleted or is too old")
		return true
	}

	switch err {
	// General errors
	// https://github.com/go-telebot/telebot/blob/8ad1e044ee330c22eb24e2ff9fdd0ed92e523648/errors.go#L69
	case tb.ErrTooLarge:
		log.Error().Err(err).Msgf("Message too large, isSendError=%v", isSendError)
		warnUnhandled(err, false)

	case tb.ErrUnauthorized:
		log.Error().Err(err).Msgf("Unauthorized in sender, isSendError=%v", isSendError)
		warnUnhandled(err, false)

	case tb.ErrNotFound:
		log.Error().Err(err).Msgf("Not found, isSendError=%v", isSendError)
		warnUnhandled(err, false)

	case tb.ErrInternal:
		log.Error().Err(err).Msgf("Internal server error, isSendError=%v", isSendError)
		warnUnhandled(err, false)

	// Bad request errors (relevant cases handled)
	// https://github.com/go-telebot/telebot/blob/8ad1e044ee330c22eb24e2ff9fdd0ed92e523648/errors.go#L77
	case tb.ErrBadButtonData:
		warnUnhandled(err, false)

	case tb.ErrCantEditMessage:
		warnUnhandled(err, false)

	case tb.ErrQueryTooOld:
		// Nothing we can de about too old queries
		return true

	case tb.ErrMessageNotModified:
		// No error, message may not be modified following a callback
		return true

	case tb.ErrSameMessageContent:
		// No error, message may not be modified following an edit
		return true
	

	case tb.ErrChatNotFound:
		warnUnhandled(err, false)

	case tb.ErrEmptyChatID:
		warnUnhandled(err, false)

	case tb.ErrEmptyMessage:
		warnUnhandled(err, false)

	case tb.ErrEmptyText:
		warnUnhandled(err, false)

	case tb.ErrGroupMigrated:
		log.Error().Err(err).Msg("Caught a tb.ErrGroupMigrated in the switch-case?")

	case tb.ErrNoRightsToSend:
		log.Debug().Msgf("No rights to send messages to chat=%s (ignoring)", chat.Id)

	case tb.ErrNoRightsToDelete:
		log.Debug().Msgf("No rights to delete message in chat=%s (ignoring)", chat.Id)

	case tb.ErrNotFoundToDelete:
		// Not really an error, as a user may have manually deleted a mesage
		return false

	case tb.ErrNoRightsToDelete:
		log.Error().Err(err).Msg("No rights to remove message in chat")
		return false

	case tb.ErrTooLongMarkup:
		warnUnhandled(err, false)

	case tb.ErrTooLongMessage:
		warnUnhandled(err, false)

	case tb.ErrWrongURL:
		warnUnhandled(err, false)

	case tb.ErrNotFoundToReply:
		warnUnhandled(err, false)

	// Forbidden errors
	// https://github.com/go-telebot/telebot/blob/8ad1e044ee330c22eb24e2ff9fdd0ed92e523648/errors.go#L122
	case tb.ErrBlockedByUser:
		log.Debug().Msgf("Bot was blocked by user=%s, removing from database...", chat.Id)
		tg.Db.RemoveUser(chat)

	case tb.ErrKickedFromGroup:
		log.Debug().Msgf("Bot was kicked from group=%s, removing from database...", chat.Id)
		tg.Db.RemoveUser(chat)

	case tb.ErrKickedFromSuperGroup:
		log.Debug().Msgf("Bot was kicked from supergroup=%s, removing from database...", chat.Id)
		tg.Db.RemoveUser(chat)

	case tb.ErrNotStartedByUser:
		log.Debug().Msgf("Bot was never started by user=%s, removing from database...", chat.Id)
		tg.Db.RemoveUser(chat)

	case tb.ErrUserIsDeactivated:
		log.Debug().Msgf("User=%s has been deactivated, removing from database...", chat.Id)
		tg.Db.RemoveUser(chat)

	case tbGroupDeleterErr:
		log.Warn().Msgf("Caught custom error tbGroupDeleterErr, deleting chat...")
		tg.Db.RemoveUser(chat)

	case tbGroupDeleterErr2:
		log.Warn().Msgf("Caught custom error tbGroupDeleterErr2, deleting chat...")
		tg.Db.RemoveUser(chat)

	default:
		// If none of the earlier switch-cases caught the error, default here
		handleGenericError(err)
	}

	return false
}
