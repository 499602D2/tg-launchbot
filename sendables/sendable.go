package sendables

import (
	"fmt"
	"launchbot/users"
	"launchbot/utils"
	"strings"
	"time"

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

/* Switches according to the recipient platform and the sendable type. */
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
func (msg *Message) SetTime(user *users.User) *string {
	// Load user's time zone, if not already loaded
	if user.Time == (users.UserTime{}) {
		user.LoadTimeZone()
	}

	// Convert unix time to local time in user's time zone
	userTime := time.Unix(msg.RefTime, 0).In(user.Time.Location)

	// Create time string, escape it
	timeString := fmt.Sprintf("%02d:%02d UTC%s",
		userTime.Hour(), userTime.Minute(), user.Time.UtcOffset)
	timeString = utils.PrepareInputForMarkdown(timeString, "text")

	// Set time, return
	txt := strings.ReplaceAll(*msg.TextContent, "$USERTIME", timeString)
	return &txt
}
