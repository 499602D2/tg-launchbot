package sendables

import (
	"fmt"
	"launchbot/users"
	"launchbot/utils"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// A sendable implements a "generic" type of a sendable object. This can be a
// notification or a command reply. These have a priority, according to which
// they will be sent.
type Sendable struct {
	Platform         string            // tg, dg
	Type             Type              // sendables.Type (Notification, Command, Callback, Delete)
	IsHighPriority   bool              // High-priority flag (anything that's not a notification)
	NotificationType string            // 24h, 12h, 1h, 5min
	LaunchId         string            // Launch ID associated with this sendable
	Message          *Message          // Message (may be nil)
	MessageIDs       map[string]string // Message ids in the form chat:msg_id for deletions
	Recipients       []*users.User     // Recipients of this sendable
	Size             int               // Size of this sendable's content, in bytes
	Tokens           int               // Amount of tokens required
	Mutex            sync.Mutex
}

// The message content of a sendable
// TODO implement an interface for messages -> TgMessage and DcMessage
type Message struct {
	TextContent string
	AddUserTime bool  // If flipped to true, TextContent contains "$USERTIME"
	RefTime     int64 // Reference time to use for replacing $USERTIME with
	SendOptions tb.SendOptions
}

type Type string

const (
	Notification Type = "notification"
	Command      Type = "command"
	Delete       Type = "delete"
)

// Load the size of the message, as perceived by Telegram's API
func (sendable *Sendable) PerceivedByteSize() int {
	if sendable.Message == nil {
		return 0
	}

	/* Some notes on just _how_ fast we can send stuff at Telegram's API

	- link tags []() do _not_ count towards the perceived byte-size of
		the message.
	- new-lines are counted as 5 bytes (!)
		- some other symbols, such as '&' or '"" may also count as 5 B

	https://telegra.ph/So-your-bot-is-rate-limited-01-26 */

	// Initialize with raw byte-count
	perceivedByteCount := len(sendable.Message.TextContent)

	// Additional 4 bytes per newline (a newline counts as 5 bytes)
	perceivedByteCount += strings.Count(sendable.Message.TextContent, "\n") * 4

	// Count &-symbols
	perceivedByteCount += strings.Count(sendable.Message.TextContent, "&") * 4

	// Calculate everything between link tags, remove from final length...?
	// Pretty easy to do, as link-tag always starts with "Watch live now" (or something)
	if strings.Contains(sendable.Message.TextContent, "http") {
		// Return the string just before the link tag starts, and after it has started
		_, afterLinkTag, found := strings.Cut(sendable.Message.TextContent, "]")

		if found {
			// Slice afterLinkTag to remove the link: [Link text]|CUT HERE|(https://...)
			linkString, _, found := strings.Cut(afterLinkTag, ")")

			if found {
				// Remove the link tag + the cut closing paranthesis (https://...)
				perceivedByteCount -= len(linkString) + 1
			}
		}
	}

	return perceivedByteCount
}

// Switches according to the recipient platform and the sendable type.
func (sendable *Sendable) Send() {
	// Loop over the users, distribute into appropriate send queues
	switch sendable.Platform {
	case "tg":
		log.Warn().Msg("Telegram message sender not implemented!")
	case "dg":
		log.Warn().Msg("Discord message sender not implemented!")
	}
}

// Set time field in the message
func SetTime(txt string, user *users.User, refTime int64, markdownPrep bool, monospace bool, dateOnly bool) string {
	// Load user's time zone, if not already loaded
	if user.Time == (users.UserTime{}) {
		user.SetTimeZone()
	}

	// Time and date in user's location
	userTime := utils.TimeInUserLocation(refTime, user.Time.Location, user.Time.UtcOffset)
	userDate := utils.DateInUserLocation(refTime, user.Time.Location)

	// Launch date in user's location
	var launchDate string

	if dateOnly {
		launchDate = fmt.Sprintf("%s", userDate)
	} else {
		launchDate = fmt.Sprintf("%s, %s", userDate, userTime)
	}

	if monospace {
		launchDate = utils.Monospaced(launchDate)
	}

	if markdownPrep {
		// Escape any markdown, if configured
		launchDate = utils.PrepareInputForMarkdown(launchDate, "text")
	}

	// Set time in the text, return
	return strings.ReplaceAll(txt, "$USERDATE", launchDate)
}

func (sendable *Sendable) AddRecipient(user *users.User, addTimeZone bool) {
	// Adds a single user to a UserList and adds a time zone if required
	sendable.Mutex.Lock()
	defer sendable.Mutex.Unlock()

	if addTimeZone {
		if user.Time == (users.UserTime{}) {
			user.SetTimeZone()
		}
	}

	// Add user to the list
	sendable.Recipients = append(sendable.Recipients, user)
}

func TextOnlySendable(txt string, user *users.User) *Sendable {
	// Construct message
	txt = fmt.Sprintf("%s", txt)
	msg := Message{
		TextContent: txt,
		SendOptions: tb.SendOptions{ParseMode: "MarkdownV2"},
	}

	// Wrap into a sendable
	sendable := Sendable{
		Type:       Command,
		Message:    &msg,
		Recipients: []*users.User{},
	}

	// Add user
	sendable.AddRecipient(user, false)

	return &sendable
}

func SendableForMessageRemoval(senderSendable *Sendable, msgIdMap map[string]string) *Sendable {
	sendable := Sendable{
		Type:       Delete,
		MessageIDs: msgIdMap,
		LaunchId:   senderSendable.LaunchId,
		Platform:   senderSendable.Platform,
		Tokens:     1,
	}

	return &sendable
}
