package ll2

import (
	"fmt"
	"launchbot/db"
	"launchbot/messages"
	"launchbot/users"
	"launchbot/utils"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dustin/go-humanize"
	emoji "github.com/jayco/go-emoji-flag"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// TODO
// - move notification functions to their own package?
// - move db.NotificationTime to notifications package?

// Creates and queues a notification
func Notify(launch *db.Launch, db *db.Database) *messages.Sendable {
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

	// Shorten long LSP names
	providerName := launch.LaunchProvider.ShortName()

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
	msg := messages.Message{
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
	sendable := messages.Sendable{
		Type: "notification", Message: &msg, Recipients: recipients,
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

// Creates a schedule message from the launch cache
// TODO simplify, now that launch cache is truly ordered (do in one loop)
func ScheduleMessage(cache *db.Cache, user *users.User, showMissions bool) string {
	if user.Time == (users.UserTime{}) {
		user.LoadTimeZone()
	}

	// List of launch-lists, one per launch date
	schedule := [][]*db.Launch{}

	// Maps in Go don't preserve order on iteration: stash the indices of each date
	dateToIndex := make(map[string]int)

	// The launch cache is always ordered (sorted when API update is parsed)
	freeIdx := 0
	for _, launch := range cache.Launches {
		// Get launch date in user's time zone
		yy, mm, dd := time.Unix(launch.NETUnix, 0).In(user.Time.Location).Date()
		dateStr := fmt.Sprintf("%d-%d-%d", yy, mm, dd)

		idx, ok := dateToIndex[dateStr]
		if ok {
			// If index exists, use it
			schedule[idx] = append(schedule[idx], launch)
		} else {
			// If 5 dates, don't add a new one
			if len(schedule) == 5 {
				continue
			}

			// Index does not exist, use first free index
			schedule = append(schedule, []*db.Launch{launch})

			// Keep track of the index we added the launch to
			dateToIndex[dateStr] = freeIdx
			freeIdx++
		}
	}

	// Map launch status to an indicator
	// https://ll.thespacedevs.com/2.2.0/config/launchstatus/
	statusToIndicator := map[string]string{
		"Partial Failure": "💥",
		"Failure":         "💥",
		"Success":         "🚀",
		"In Flight":       "🚀",
		"Hold":            "⏸️",
		"Go":              "🟢", // Go, as in a verified launch time
		"TBC":             "🟡", // Unconfirmed launch time
		"TBD":             "🔴", // Unverified launch time
	}

	// Loop over the schedule map and create the message
	msg := "📅 *5-day flight schedule*\n" +
		fmt.Sprintf("_Dates are relative to UTC%s. ", user.Time.UtcOffset) +
		"For detailed flight information, use /next@rocketrybot._\n\n"

	i := 0
	for _, launchList := range schedule {
		// Add date header
		userLaunchTime := time.Unix(launchList[0].NETUnix, 0).In(user.Time.Location)

		// Time until launch date, relative to user's time zone
		userNow := time.Now().In(user.Time.Location)
		timeToLaunch := userLaunchTime.Sub(userNow)

		// ETA string, e.g. "today", "tomorrow", or "in 5 days"
		etaString := ""

		// See if eta + userNow is still the same day
		if userNow.Add(timeToLaunch).Day() == userNow.Day() {
			// Same day: launch is today
			etaString = "today"
		} else {
			// If the day is not the same, do simple subtraction
			// Remove 24-hour modulo from the time: this leaves us with whole days,
			// and since the day _has_ to be at least one day from now, the rest is trivial
			// humanize.Ordinal(...)
			secUntil := int64(timeToLaunch.Seconds()) - int64(timeToLaunch.Seconds())%(3600*24)
			daysUntil := secUntil / (3600 * 24) // 0 == tomorrow, 1 == 2 days from now, etc.

			// TODO fix
			//log.Debug().Msgf("secUntil: %d | daysUntil: %d", secUntil, daysUntil)

			if daysUntil == 0 {
				etaString = "tomorrow"
			} else {
				etaString = fmt.Sprintf("in %d days", daysUntil+1)
			}
		}

		// The header of the date, e.g. "June 1st in 7 days"
		header := fmt.Sprintf("*%s %s* %s\n",
			userLaunchTime.Month().String(),
			humanize.Ordinal(userLaunchTime.Day()),
			etaString,
		)

		// Add header
		msg += header

		// Loop over launches, add
		var row string
		for i, launch := range launchList {
			if i == 3 {
				msg += fmt.Sprintf("*+ %d more flights*\n", len(launchList)-i)
				break
			}

			if !showMissions {
				row = fmt.Sprintf("%s%s %s %s",
					emoji.GetFlag(launch.LaunchProvider.CountryCode),
					statusToIndicator[launch.Status.Abbrev],
					launch.LaunchProvider.ShortName(),
					launch.Rocket.Config.Name)
			} else {
				missionName := launch.Mission.Name
				if missionName == "" {
					missionName = "Unknown payload"
				}

				row = fmt.Sprintf("%s%s %s",
					emoji.GetFlag(launch.LaunchProvider.CountryCode),
					statusToIndicator[launch.Status.Abbrev], missionName)
			}

			msg += row + "\n"
		}

		i += 1
		if i != len(schedule) {
			msg += "\n"
		}
	}

	// Add the footer
	msg += "\n" + "🟢🟡🔴 *Launch-time accuracy*"

	// Escape the message, return
	msg = utils.PrepareInputForMarkdown(msg, "text")
	return msg
}
