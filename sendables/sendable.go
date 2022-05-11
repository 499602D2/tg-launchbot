package sendables

import (
	"fmt"
	"launchbot/users"
	"launchbot/utils"
	"strings"

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
	RateLimit  int             // Ratelimits this sendable should obey
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
func (sendable *Sendable) CalculateTgApiByteSize() float32 {
	return 0
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
		RateLimit:  5.0,
		Message:    &msg,
		Recipients: &users.UserList{},
	}

	// Add user
	sendable.Recipients.Add(user, false)

	return &sendable
}
