package ll2

import (
	"fmt"
	"launchbot/bots"
	"launchbot/db"
	"launchbot/users"
	"launchbot/utils"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	emoji "github.com/jayco/go-emoji-flag"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

type NotificationTime struct {
	Type       string // In (24hour, 12hour, 1hour, 5min)
	SendTime   int64  // Unix-time of the notification
	AllSent    bool   // All notifications sent already?
	LaunchId   string
	LaunchName string

	Count int      // If more than one, list their count
	IDs   []string // If more than one, include their IDs here
}

// func (cache *LaunchCache) FindAllWithNet(net int64) []*Launch {
// 	launches := []*Launch{}

// 	for _, launch := range cache.Launches {

// 	}
// }

/* Returns the first unsent notification type for a launch. */
func (launch *Launch) NextNotification() NotificationTime {
	// TODO do this smarter instead of re-declaring a billion times
	NotificationSendTimes := map[string]time.Duration{
		"24hour": time.Duration(24) * time.Hour,
		"12hour": time.Duration(12) * time.Hour,
		"1hour":  time.Duration(1) * time.Hour,
		"5min":   time.Duration(5) * time.Minute,
	}

	// Minutes the send-time is allowed to slip by
	allowedSlipMins := 5

	for notifType, sent := range launch.Notifications {
		// Map starts from 24hour, goes down to 5min
		if sent == false {
			// How many seconds before NET the notification is sent
			secBeforeNet, ok := NotificationSendTimes[notifType]

			if !ok {
				log.Error().Msgf("Error parsing notificationType for %s: %s",
					launch.Id, notifType)
				continue
			}

			// Calculate send-time from NET
			// log.Debug().Msgf("type: %s, secBeforeNet: %s", notifType, secBeforeNet.String())
			// log.Debug().Msgf("NET time: %s", time.Unix(launch.NETUnix, 0).String())
			sendTime := launch.NETUnix - int64(secBeforeNet.Seconds())

			if sendTime-time.Now().Unix() < 0 {
				// Calculate how many minutes the notification was missed by
				missedBy := time.Duration(math.Abs(float64(time.Now().Unix()-sendTime))) * time.Second

				// TODO implement launch.ClearMissedNotifications + database update
				if missedBy > time.Minute*time.Duration(allowedSlipMins) {
					// TODO set as missed in database
					log.Warn().Msgf("[%s] Missed type=%s notification (by %.2f minutes)",
						launch.Slug, notifType, missedBy.Minutes())
					continue
				} else {
					log.Info().Msgf("[%s] Missed type=%s by under %d min (%.2f min): modifying send-time",
						launch.Slug, notifType, allowedSlipMins, missedBy.Minutes())

					// Modify to send in 5 seconds
					sendTime = time.Now().Unix() + 5

					return NotificationTime{Type: notifType, SendTime: sendTime,
						LaunchId: launch.Id, LaunchName: launch.Name, Count: 1}
				}
			}

			// Sent is false and has not been missed: return type
			return NotificationTime{Type: notifType, SendTime: sendTime,
				LaunchId: launch.Id, LaunchName: launch.Name, Count: 1}
		}
	}

	// No unsent notifications: return with AllSent=true
	return NotificationTime{AllSent: true, LaunchId: launch.Id, LaunchName: launch.Name, Count: 0}
}

/*
Finds the next notification send-time from the launch cache.

Function goes over the notification states and finds the next notification
to send, returning a NotificationTime type with the send-time and all launch
IDs associated with this send-time. */
func (cache *LaunchCache) FindNext() *NotificationTime {
	// Find first send-time from the launch cache
	earliestTime := int64(0)
	tbdLaunchCount := 0

	/* Returns a list of notification times
	(only more than one if two+ notifs share the same send time) */
	notificationTimes := make(map[int64][]NotificationTime)

	// How much the send time is allowed to slip, in minutes
	allowedNetSlip := time.Duration(-5) * time.Minute

	for _, launch := range cache.Launches {
		// If launch time is TBD/TBC or in the past, don't notify
		if launch.Status.Abbrev == "Go" {
			// Calculate the next upcoming send time for this launch
			next := launch.NextNotification()

			if next.AllSent {
				// If all notifications have already been sent, ignore
				// log.Warn().Msgf("All notifications have been sent for launch=%s", launch.Id)
				continue
			}

			// Verify the launch-time is not in the past by more than the allowed slip window
			if allowedNetSlip.Seconds() > time.Until(time.Unix(next.SendTime, 0)).Seconds() {
				log.Warn().Msgf("Launch %s is more than 5 minutes into the past",
					next.LaunchName)
				continue
			}

			if (next.SendTime < earliestTime) || (earliestTime == 0) {
				// If time is smaller than last earliestTime, delete old key and insert
				delete(notificationTimes, earliestTime)
				earliestTime = next.SendTime

				// Insert into the map's list
				notificationTimes[next.SendTime] = append(notificationTimes[next.SendTime], next)
			} else if next.SendTime == earliestTime {
				// Alternatively, if the time is equal, we have two launches overlapping
				notificationTimes[next.SendTime] = append(notificationTimes[next.SendTime], next)
			}
		} else {
			tbdLaunchCount++
		}
	}

	// If time is non-zero, there's at least one non-TBD launch
	if earliestTime != 0 {
		// Calculate time until notification(s)
		toNotif := time.Until(time.Unix(earliestTime, 0))

		log.Debug().Msgf("Got next notification send time (%s from now), %d launches)",
			toNotif.String(), len(notificationTimes[earliestTime]))

		// Print launch names in logs
		for n, l := range notificationTimes[earliestTime] {
			log.Debug().Msgf("[%d] %s (%s)", n+1, l.LaunchName, l.LaunchId)
		}
	} else {
		log.Warn().Msgf("Could not find next notification send time. No-Go launches: %d out of %d",
			tbdLaunchCount, len(cache.Launches))

		return &NotificationTime{SendTime: 0, Count: 0}
	}

	// Select the list of launches for the earliest timestamp
	notificationList := notificationTimes[earliestTime]

	// If more then one, prioritize them
	if len(notificationList) > 1 {
		// Add more weight to the latest notifications
		timeWeights := map[string]int{
			"24hour": 1, "12hour": 2,
			"1hour": 3, "5min": 4,
		}

		// Keep track of largest encountered key (timeWeight)
		maxTimeWeight := 0

		// Map the weights to a single NotificationTime type
		weighedNotifs := make(map[int]NotificationTime)

		// Loop over the launches we found at this timestamp
		for _, notifTime := range notificationList {
			// Add to the weighed map
			weighedNotifs[timeWeights[notifTime.Type]] = notifTime

			// If weight is greater than the largest encountered, update
			if timeWeights[notifTime.Type] > maxTimeWeight {
				maxTimeWeight = timeWeights[notifTime.Type]
			}
		}

		// Assign highest-value key found as the primary notification
		firstNotif := weighedNotifs[maxTimeWeight]
		firstNotif.Count = len(notificationList)
		firstNotif.IDs = append(firstNotif.IDs, firstNotif.LaunchId)

		// Add other launches to the list
		for _, notifTime := range notificationList {
			if notifTime.LaunchId != firstNotif.LaunchId {
				firstNotif.IDs = append(firstNotif.IDs, notifTime.LaunchId)
			}
		}

		log.Debug().Msgf("Total of %d launches in the notification list after parsing:",
			len(firstNotif.IDs))

		for i, id := range firstNotif.IDs {
			log.Debug().Msgf("[%d] %s", i+1, id)
		}

		return &firstNotif
	}

	// Otherwise, we only have one notification: return it
	onlyNotif := notificationList[0]
	onlyNotif.IDs = append(onlyNotif.IDs, onlyNotif.LaunchId)
	return &onlyNotif
}

/*
Extends the Launch struct to add a .PostponeNotify() method.
This allows us to write cleaner code.
*/
func (launch *Launch) PostponeNotify(postponedTo int) {
}

/* Pulls recipients for this notification type from the DB */
func (launch *Launch) GetRecipients(db *db.Database, notifType NotificationTime) *users.UserList {
	// TODO Implement
	recipients := users.UserList{Platform: "tg", Users: []*users.User{}}
	user := users.User{Platform: recipients.Platform, Id: db.Owner}

	recipients.Add(user, true)

	return &recipients
}

/* Creates and queues a notification */
func (launch *Launch) Notify(db *db.Database) *bots.Sendable {
	// TODO for the message construction: use "real ETA" for 5 min notification

	notification := launch.NextNotification()

	// Map notification type to a header
	header := map[string]string{
		"24hour": "in 24 hours",
		"12hour": "in 12 hours",
		"1hour":  "in 60 minutes",
		"5min":   "in 5 minutes",
	}[notification.Type]

	// If notification type is 5-minute, use real launch time for clarity
	if notification.Type == "5min" {
		untilNet := time.Until(time.Unix(launch.NETUnix, 0))

		// If we're seconds away, use seconds
		if untilNet.Minutes() < 1.0 {
			log.Warn().Msgf("Launch less than 1 minute away, formatting as seconds")

			// If less than 0 seconds, set to "now"
			if untilNet.Seconds() < 0 {
				header = "now"
			} else {
				header = fmt.Sprintf("in %d seconds", int(untilNet.Seconds()))
			}
		} else {
			header = fmt.Sprintf("in %d minutes", int(untilNet.Minutes()))
		}
	}

	name := launch.Mission.Name
	if name == "" {
		name = strings.Trim(strings.Split(launch.Name, "|")[0], " ")
	}

	/*
		Do a simple, low-data notification string. Example:

		Crew-4 is launching in 5 minutes 🚀
		Provider SpaceX 🇺🇸
		From Cape Canaveral LC-39A 🇺🇸

		Mission information 🌍
		Type Tourism
		Orbit Low-Earth orbit
		Lift-off at 18:17 UTC+3

		🔴 Watch live
		🔕 Stop with /notify@rocketrybot

		btn[🔇 Mute launch]
		btn[ℹ️ Extend description]

		===========================

		-> [ℹ️ Extend description]:

		T-5 minutes: JWST 🚀
		Provider SpaceX 🇺🇸
		From Cape Canaveral LC-39A 🇺🇸

		Mission information 🌍
		Type Tourism
		Orbit Low-Earth orbit
		Lift-off at 18:17 UTC+3

		Vehicle information 🚀
		Falcon 9 B1062.5 (♻️x4)
		Landing on ASOG (ASDS)

		ℹ️ The James Webb Space Telescope is a space
		telescope developed by NASA, ESA and CSA to
		succeed the Hubble Space Telescope as NASA's
		flagship astrophysics mission.

	*/

	// Shorten long LSP names
	providerName := launch.LaunchProvider.Name
	if len(providerName) > len("Virgin Galactic") {
		short, ok := LSPShorthands[providerName]
		if ok {
			providerName = short
		} else {
			log.Warn().Msgf("Provider name '%s' is long, but not found in LSPShorthands", providerName)
		}
	}

	// Get country-flag
	flag := ""
	if launch.LaunchProvider.CountryCode != "" {
		flag = " " + emoji.GetFlag(launch.LaunchProvider.CountryCode)
	}

	/* Ideas
	- modify mission information header according to type
		- comms is a satellite, crew is an astronaut, etc.
	*/

	// TODO create a copy of the launch -> monospace all relevant fields?
	// TODO set bot username dynamically (throw a tb.bot object at ll2.notify?)

	text := fmt.Sprintf(
		"🚀 *%s is launching %s*\n"+
			"*Provider* %s%s\n"+
			"*Rocket* %s\n"+
			"*From* %s\n\n"+

			"🌍 *Mission information*\n"+
			"*Type* %s\n"+
			"*Orbit* %s\n\n"+

			"🕑 *Lift-off at $USERTIME*\n"+
			"🔴 *LAUNCHLINKGOESHERE*\n"+
			"🔕 *Stop with /notify@tglaunchbot*",

		name, header,

		utils.Monospaced(providerName), flag, utils.Monospaced(launch.Rocket.Config.FullName),
		utils.Monospaced(launch.LaunchPad.Name),

		utils.Monospaced(launch.Mission.Type), utils.Monospaced(launch.Mission.Orbit.Name),
	)

	// Prepare message for Telegram's MarkdownV2 parser
	text = utils.PrepareInputForMarkdown(text, "text")

	// TODO set link properly (parse by importance and set no-vid-available)
	linkText := utils.PrepareInputForMarkdown("Watch launch live!", "text")
	link := utils.PrepareInputForMarkdown("https://www.youtube.com/watch?v=5nLk_Vqp7nw", "link")
	launchLink := fmt.Sprintf("[%s](%s)", linkText, link)
	text = strings.Replace(text, "LAUNCHLINKGOESHERE", launchLink, 1)

	log.Debug().Msgf("Notification created: %d runes, %d bytes",
		utf8.RuneCountInString(text), len(text))

	// Trim whitespace and all the other funny stuff that comes with multi-lining
	// https://stackoverflow.com/questions/37290693/how-to-remove-redundant-spaces-whitespace-from-a-string-in-golang

	// Send silently if not a 1-hour or 5-minute notification
	sendSilently := true
	if notification.Type == "1hour" || notification.Type == "5min" {
		sendSilently = false
	}

	// TODO: implement callback handling
	muteBtn := tb.InlineButton{
		Text: "🔇 Mute launch",
		Data: fmt.Sprintf("mute/%s", launch.Id),
	}

	// TODO: implement callback handling
	expandBtn := tb.InlineButton{
		Text: "ℹ️ Expand description",
		Data: fmt.Sprintf("exp/%s", launch.Id),
	}

	// Construct the keeb
	kb := [][]tb.InlineButton{
		{muteBtn}, {expandBtn},
	}

	// Message
	msg := bots.Message{
		TextContent: &text,
		AddUserTime: true,
		RefTime:     launch.NETUnix,
		SendOptions: tb.SendOptions{
			ParseMode:           "MarkdownV2",
			DisableNotification: sendSilently,
			ReplyMarkup:         &tb.ReplyMarkup{InlineKeyboard: kb},
		},
	}

	// TODO Get recipients
	recipients := launch.GetRecipients(db, notification)

	/*
		Some notes on just _how_ fast we can send stuff at Telegram's API

		- link tags []() do _not_ count towards the perceived byte-size of
			the message.
		- new-lines are counted as 5 bytes (!)
			- some other symbols, such as '&' or '"" may also count as 5 B

		https://telegra.ph/So-your-bot-is-rate-limited-01-26
	*/

	/* Set rate-limit based on text length
	TODO count markdown, ignore links (insert link later?)
	- does markdown formatting count? */
	perceivedByteLen := len(text)
	perceivedByteLen += strings.Count(text, "\n") * 4 // Additional 4 B per newline

	rateLimit := 30
	if perceivedByteLen >= 512 {
		// TODO update bot's limiter...?
		log.Warn().Msgf("Large message (%d bytes): lowering send-rate to 6 msg/s", perceivedByteLen)
		rateLimit = rateLimit / 5
	}

	// Create sendable
	sendable := bots.Sendable{
		Priority: 1, Type: "notification", Message: &msg, Recipients: recipients,
		RateLimit: rateLimit,
	}

	/*
		Loop over the sent-flags, and ensure every previous state is flagged.
		This is important for launches that come out of the blue, namely launches
		by e.g. China/Chinese companies, where the exact NET may only appear less
		than 24 hours before lift-off.

		As an example, the first notification we send might be the 1-hour notification.
		In this case, we will need to flag the 12-hour and 24-hour notification types
		as sent, as they are no-longer relevant. This is done below.
	*/

	iterMap := map[string]string{
		"5min":   "1hour",
		"1hour":  "12hour",
		"12hour": "24hour",
	}

	// Toggle the current state as sent, after which all flags will be toggled
	passed := false
	for curr, next := range iterMap {
		if !passed && curr == notification.Type {
			// This notification was sent: set state
			launch.Notifications[curr] = true
			passed = true
		}

		if passed {
			// If flag has been set, and last type is flagged as unsent, update flag
			if launch.Notifications[next] == false {
				log.Debug().Msgf("Set %s to true for launch=%s", next, launch.Id)
				launch.Notifications[next] = true
			}
		}
	}

	// TODO do database update...?

	return &sendable
}

/* Returns all values for a database insert */
func (launch *Launch) FieldValues() {
	// TODO complete
}
