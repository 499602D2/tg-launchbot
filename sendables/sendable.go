package sendables

import (
	"fmt"
	"launchbot/users"
	"launchbot/utils"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// A sendable implements a "generic" type of a sendable object. This can be a
// notification or a command reply. These have a priority, according to which
// they will be sent.
type Sendable struct {
	Type       string          // in ("remove", "notification", "command", "callback")
	Message    *Message        // Message (may be empty)
	Recipients *users.UserList // Recipients of this sendable
	Tokens     int             // Amount of tokens required
	LaunchId   string          // Launch ID associated with this sendable
}

// The message content of a sendable
// TODO implement an interface for messages -> TgMessage and DscMessage
type Message struct {
	TextContent *string
	AddUserTime bool  // If flipped to true, TextContent contains "$USERTIME"
	RefTime     int64 // Reference time to use for replacing $USERTIME with
	SendOptions tb.SendOptions
}

// TODO implement so limiter can have more granularity and avoid rate-limits
func (sendable *Sendable) PerceivedByteSize() int {
	// Raw rune- and byte-count
	runeCount := utf8.RuneCountInString(*sendable.Message.TextContent)
	byteCount := len(*sendable.Message.TextContent)

	/* Some notes on just _how_ fast we can send stuff at Telegram's API

	- link tags []() do _not_ count towards the perceived byte-size of
		the message.
	- new-lines are counted as 5 bytes (!)
		- some other symbols, such as '&' or '"" may also count as 5 B

	https://telegra.ph/So-your-bot-is-rate-limited-01-26 */

	/* Set rate-limit based on text length
	- TODO count markdown, ignore links (calculate before link insertion? Ignore link tag?)
	- does markdown formatting count?
	- other considerations? */
	perceivedByteCount := byteCount

	// Additional 4 bytes per newline (a newline counts as 5 bytes)
	perceivedByteCount += strings.Count(*sendable.Message.TextContent, "\n") * 4

	// Count &-symbols
	perceivedByteCount += strings.Count(*sendable.Message.TextContent, "&") * 4

	// Calculate everything between link tags, remove from final length...?
	// Pretty easy to do, as link-tag always starts with "Watch live now" (or something)

	log.Debug().Msgf("Rune-count: %d, byte-count: %d, perceived: %d",
		runeCount, byteCount, perceivedByteCount)

	return perceivedByteCount
}

// Switches according to the recipient platform and the sendable type.
func (sendable *Sendable) Send() {
	// Loop over the users, distribute into appropriate send queues
	switch sendable.Recipients.Platform {
	case "tg":
		log.Warn().Msg("Telegram message sender not implemented!")
	case "dg":
		log.Warn().Msg("Discord message sender not implemented!")
	}
}

// Set time field in the message
func SetTime(txt string, user *users.User, refTime int64, markdownPrep bool) *string {
	// Load user's time zone, if not already loaded
	if user.Time == (users.UserTime{}) {
		user.SetTimeZone()
	}

	// Get time string in user's location
	timeString := utils.TimeInUserLocation(refTime, user.Time.Location, user.Time.UtcOffset)

	// Monospace
	timeString = utils.Monospaced(timeString)

	if markdownPrep {
		timeString = utils.PrepareInputForMarkdown(timeString, "text")
	}

	// Set time, return
	txt = strings.ReplaceAll(txt, "$USERTIME", timeString)
	return &txt
}

func TextOnlySendable(txt string, user *users.User) *Sendable {
	// Construct message
	txt = fmt.Sprintf("%s", txt)
	msg := Message{
		TextContent: &txt,
		SendOptions: tb.SendOptions{ParseMode: "MarkdownV2"},
	}

	// Wrap into a sendable
	sendable := Sendable{
		Type:       "command",
		Message:    &msg,
		Recipients: &users.UserList{},
	}

	// Add user
	sendable.Recipients.Add(user, false)

	return &sendable
}
