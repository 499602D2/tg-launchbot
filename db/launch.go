package db

import (
	"fmt"
	"launchbot/sendables"
	"launchbot/users"
	"launchbot/utils"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dustin/go-humanize"
	"github.com/dustin/go-humanize/english"
	"github.com/hako/durafmt"
	emoji "github.com/jayco/go-emoji-flag"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
	"gorm.io/gorm"
)

type LaunchUpdate struct {
	Count    int
	Launches []*Launch `json:"results"`
}

type Launch struct {
	// Information we get from the API
	Id             string         `json:"id" gorm:"primaryKey;uniqueIndex"`
	Name           string         `json:"name"`
	Slug           string         `json:"slug"`
	Status         LaunchStatus   `json:"status" gorm:"embedded;embeddedPrefix:status_"`
	LastUpdated    string         `json:"last_updated"`
	NET            string         `json:"net"`
	WindowEnd      string         `json:"window_end"`
	WindowStart    string         `json:"window_start"`
	Probability    int            `json:"probability"`
	HoldReason     string         `json:"holdreason"`
	FailReason     string         `json:"failreason"`
	LaunchProvider LaunchProvider `json:"launch_service_provider" gorm:"embedded;embeddedPrefix:provider_"`
	Rocket         Rocket         `json:"rocket" gorm:"embedded;embeddedPrefix:rocket_"`
	Mission        Mission        `json:"mission" gorm:"embedded;embeddedPrefix:mission_"`
	LaunchPad      LaunchPad      `json:"pad" gorm:"embedded;embeddedPrefix:pad_"`
	WebcastIsLive  bool           `json:"webcast_live"`
	LL2Url         string         `json:"url"`

	NETUnix     int64     // Calculated from NET
	Launched    bool      // True if success/failure/partial_failure
	WebcastLink string    // Highest-priority link from VidURLs
	ApiUpdate   time.Time // Time of last API update

	// Status of notification sends (boolean + non-embedded map)
	NotificationState NotificationState `gorm:"embedded"`

	// Track IDs of previously sent notifications (comma-separated string of message IDs)
	SentNotificationIds string

	// Information not dumped into the database (-> manual parse on a cache init from db)
	InfoURL []ContentURL `json:"infoURLs" gorm:"-:all"` // Not embedded into db: parsed manually
	VidURL  []ContentURL `json:"vidURLs" gorm:"-:all"`  // Not embedded into db: parsed manually

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// Maps the send times to send states.
type NotificationState struct {
	Sent24h  bool
	Sent12h  bool
	Sent1h   bool
	Sent5min bool

	// Maps send-times to boolean states.
	// Keys are equal to the NotificationState boolean fields.
	Map map[string]bool `gorm:"-:all"`
}

type LaunchStatus struct {
	Id          int    `json:"id"`
	Name        string `json:"name"`
	Abbrev      string `json:"abbrev"`
	Description string `json:"description"`
}

type LaunchProvider struct {
	// Information directly from the API
	Id          int    `json:"id"`
	Name        string `json:"name"`
	Abbrev      string `json:"abbrev"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	CountryCode string `json:"country_code"`
}

type Rocket struct {
	Id     int                 `json:"id"`
	Config RocketConfiguration `json:"configuration" gorm:"embedded;embeddedPrefix:config_"`

	/*
		TODO: add missing properties
		- add launcher_stage
		- add spacecraft_stage
	*/
}

type RocketConfiguration struct {
	Id       int    `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Variant  string `json:"variant"`

	/* Optional:
	- add total_launch_count
	- add consecutive_successful_launches
	*/
}

type Mission struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Orbit       Orbit  `json:"orbit" gorm:"embedded;embeddedPrefix:orbit_"`
}

type Orbit struct {
	Id     int    `json:"id"`
	Name   string `json:"name"`   // e.g. "Low Earth Orbit"
	Abbrev string `json:"abbrev"` // e.g. "LEO"
}

type LaunchPad struct {
	Name             string      `json:"name"`
	Location         PadLocation `json:"location" gorm:"embedded;embeddedPrefix:location_"`
	TotalLaunchCount int         `json:"total_launch_count"`
}

type PadLocation struct {
	Name             string `json:"name"`
	CountryCode      string `json:"country_code"`
	TotalLaunchCount int    `json:"total_launch_count"`
}

type ContentURL struct {
	Priority    int    `json:"priority"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Url         string `json:"url"`
}

// Generates the message content used by both notifications and /next
func (launch *Launch) MessageBodyText(expanded bool, isNotification bool) string {
	var (
		flag              string
		location          string
		description       string
		launchTimeSection string
	)

	// Shorten long LSP names
	providerName := launch.LaunchProvider.ShortName()

	// Get country-flag
	if launch.LaunchProvider.CountryCode != "" {
		flag = " " + emoji.GetFlag(launch.LaunchProvider.CountryCode)
	}

	// Get a string for the launch location
	if launch.LaunchPad.Location.CountryCode != "" {
		if launch.LaunchPad.Location.Name != "" {
			location = strings.Split(launch.LaunchPad.Location.Name, ",")[0] + " "
		}

		location = fmt.Sprintf(", %s%s", location, emoji.GetFlag(launch.LaunchPad.Location.CountryCode))
	}

	if !isNotification {
		// If not a notification, add the "Launch time" section with date and time
		if launch.Status.Abbrev == "TBD" {
			// If launch-time is still TBD, add a Not-earlier-than date and reduce time accuracy
			timeUntil := fmt.Sprintf("%s", durafmt.Parse(time.Until(time.Unix(launch.NETUnix, 0))).LimitFirstN(1))

			launchTimeSection = fmt.Sprintf(
				"🕙 *Launch time*\n"+
					"*No earlier than* $USERDATE\n"+
					"*Until launch* %s\n\n",

				utils.Monospaced(timeUntil),
			)
		} else {
			// Otherwise, the date is close enough
			timeUntil := fmt.Sprintf("%s", durafmt.Parse(time.Until(time.Unix(launch.NETUnix, 0))).LimitFirstN(2))

			launchTimeSection = fmt.Sprintf(
				"🕙 *Launch time*\n"+
					"*Date* $USERDATE\n"+
					"*Until launch* %s\n\n",

				utils.Monospaced(timeUntil),
			)
		}
	} else {
		launchTimeSection = ""
	}

	// Mission information
	missionType := launch.Mission.Type
	missionOrbit := launch.Mission.Orbit.Name

	if missionType == "" {
		missionType = "Unknown purpose"
	}

	if missionOrbit == "" {
		missionOrbit = "Unknown orbit"
	}

	if expanded {
		// If this is an expanded message, add a description
		if launch.Mission.Description == "" {
			description = "ℹ️ No information available\n\n"
		} else {
			description = fmt.Sprintf("ℹ️ %s\n\n", launch.Mission.Description)
		}
	}

	text := fmt.Sprintf(
		"*Provider* %s%s\n"+
			"*Rocket* %s\n"+
			"*From* %s%s\n\n"+

			"%s"+

			"🌍 *Mission information*\n"+
			"*Type* %s\n"+
			"*Orbit* %s\n\n"+

			"%s",

		utils.Monospaced(providerName), flag, utils.Monospaced(launch.Rocket.Config.FullName),
		utils.Monospaced(launch.LaunchPad.Name), utils.Monospaced(location),

		launchTimeSection,

		utils.Monospaced(missionType), utils.Monospaced(missionOrbit),

		description,
	)

	return text
}

// Produces a launch notification message
func (launch *Launch) NotificationMessage(notifType string, expanded bool) string {
	// Map notification type to a header
	header, ok := map[string]string{
		"24h": "T-24 hours", "12h": "T-12 hours",
		"1h": "T-60 minutes", "5min": "T-5 minutes",
	}[notifType]

	if !ok {
		log.Warn().Msgf("%s not found when mapping notif.Type to header", notifType)
	}

	// If notification type is 5-minute, use real launch time for clarity
	if notifType == "5min" && !expanded {
		untilNet := time.Until(time.Unix(launch.NETUnix, 0))

		// If we're seconds away, use seconds
		if untilNet.Minutes() < 1.0 {
			log.Warn().Msgf("Launch less than 1 minute away, formatting as seconds")

			// If less than 0 seconds, set to "now"
			if untilNet.Seconds() < 0 {
				header = "Launching now"
			} else {
				header = fmt.Sprintf("T-%d seconds", int(untilNet.Seconds()))
			}
		} else {
			header = fmt.Sprintf("T-%d minutes", int(untilNet.Minutes()))
		}
	}

	// Load a name for the launch
	name := launch.HeaderName()

	// Only add the webcast link for 1-hour and 5-minute notifications
	var webcastLink string
	if notifType == "1h" || notifType == "5min" {
		if launch.WebcastLink != "" {

			// Prepare link for markdown
			link := utils.PrepareInputForMarkdown(launch.WebcastLink, "link")

			// Format into a markdown link
			linkText := utils.PrepareInputForMarkdown("Watch launch live!", "text")
			webcastLink = fmt.Sprintf("🔴 [*%s*](%s)\n", linkText, link)
		} else {
			// No video available
			webcastLink = "🔇 *No live video available*\n"
		}
	} else {
		webcastLink = ""
	}

	// Load message body
	messageBody := launch.MessageBodyText(expanded, true)

	// TODO add bot username dynamically
	text := fmt.Sprintf(
		"🚀 *%s: %s*\n"+
			"%s"+

			"🕙 *$USERDATE*\n"+
			"LAUNCHLINKHERE"+
			"🔕 *Stop with /settings@tglaunchbot*",

		// Name, launching-in, provider, rocket, launch pad
		header, name, messageBody,
	)

	// Prepare message for Telegram's MarkdownV2 parser
	text = utils.PrepareInputForMarkdown(text, "text")

	// Add a launch link if required
	text = strings.ReplaceAll(text, "LAUNCHLINKHERE", webcastLink)

	if !expanded {
		log.Debug().Msgf("Notification created: %d runes, %d bytes",
			utf8.RuneCountInString(text), len(text))
	}

	return text
}

// Creates a schedule message from the launch cache
func (cache *Cache) ScheduleMessage(user *users.User, showMissions bool) string {
	// List of launch-lists, one list per launch date
	schedule := [][]*Launch{}

	// Maps in Go don't preserve order, so keep track of the index they are at.
	// Also helps us trivially track which dates have been added
	dateToIndex := make(map[string]int)

	// Loop over all launches and build a launchDate:listOfLaunches map
	for _, launch := range cache.Launches {
		// Ignore bad launches (only really with LL2's development endpoint)
		delta := time.Until(time.Unix(launch.NETUnix, 0))
		if delta.Seconds() < 0 && launch.Status.Abbrev != "In Flight" {
			continue
		}

		// Get launch date in user's time zone
		yy, mm, dd := time.Unix(launch.NETUnix, 0).In(user.Time.Location).Date()
		dateStr := fmt.Sprintf("%d-%d-%d", yy, mm, dd)

		// Keep track of dates we have in the message
		idx, ok := dateToIndex[dateStr]

		if ok {
			// If this date has already been added, use the existing index
			schedule[idx] = append(schedule[idx], launch)
		} else {
			// Date does not exist: add, unless we already have five dates, in which case break the loop
			if len(schedule) == 5 {
				break
			}

			// Index does not exist, use first free index
			schedule = append(schedule, []*Launch{launch})

			// Keep track of the index we added the launch-date to
			dateToIndex[dateStr] = len(schedule) - 1
		}
	}

	// User message
	// TODO add bot username dynamically to the command
	message := "📅 *5-day flight schedule*\n" +
		fmt.Sprintf("_Dates are relative to %s. ", user.Time.UtcOffset) +
		"For detailed flight information, use /next._\n\n"

	// Loop over the created map and create the message
	for listIterCount, launchList := range schedule {
		// Add date header
		userLaunchTime := time.Unix(launchList[0].NETUnix, 0).In(user.Time.Location)

		// Time until launch date, relative to user's time zone
		userNow := time.Now().In(user.Time.Location)
		userEta := userLaunchTime.Sub(userNow)

		// Get a friendly ETA string (e.g. "tomorrow", "in 2 days")
		etaString := utils.FriendlyETA(userNow, userEta)

		// The header of the date, e.g. "June 1st, in 7 days"
		header := fmt.Sprintf("*%s %s* %s\n",
			userLaunchTime.Month().String(), humanize.Ordinal(userLaunchTime.Day()), etaString,
		)

		// Add header to the schedule message
		message += header

		// Loop over launches, add
		var row string
		for i, launch := range launchList {
			if i == 3 {
				message += fmt.Sprintf("*+ %d more %s*\n", len(launchList)-i, english.Plural(int(len(launchList)-i), "flight", "flights"))
				break
			}

			if !showMissions {
				// Create the row (vehicle-mode): status indicator, flag, provider name, rocket name
				row = fmt.Sprintf("%s%s %s %s", utils.Monospaced(utils.StatusNameToIndicator[launch.Status.Abbrev]), emoji.GetFlag(launch.LaunchProvider.CountryCode),
					utils.Monospaced(launch.LaunchProvider.ShortName()), utils.Monospaced(launch.Rocket.Config.Name))
			} else {
				// Create the row (mission-mode)
				missionName := launch.Mission.Name

				if missionName == "" {
					missionName = "Unknown payload"
				}

				// Status indicator, flag, mission name
				row = fmt.Sprintf("%s%s %s", utils.Monospaced(utils.StatusNameToIndicator[launch.Status.Abbrev]),
					emoji.GetFlag(launch.LaunchProvider.CountryCode), utils.Monospaced(missionName))
			}

			message += row + "\n"
		}

		// If not the last list, add a newline
		if listIterCount != len(schedule) {
			message += "\n"
		}
	}

	// Add the footer
	message += "🟢🟡🔴 *Launch-time accuracy*"

	// Escape the message, return it
	return utils.PrepareInputForMarkdown(message, "italictext")
}

// Creates the text content and Telegram reply keyboard for the /next command.
func (cache *Cache) NextLaunchMessage(user *users.User, index int) string {
	// Pull launch from cache at index
	launch := cache.Launches[index]

	// If cache has old launches, refresh it
	if launch.NETUnix < time.Now().Unix() {
		cache.Populate()
		launch = cache.Launches[index]
	}

	// If mission has no name, use the name of the launch itself (and split by `|`)
	name := launch.HeaderName()

	text := fmt.Sprintf(
		"🚀 *Next launch* %s\n"+
			"%s",

		utils.Monospaced(name), launch.MessageBodyText(true, false),
	)

	// Check if the launch date is TBD, and we should use the low-accuracy NET date
	dateOnly := false
	if launch.Status.Abbrev == "TBD" {
		dateOnly = true
	}

	// Set user's time
	text = sendables.SetTime(text, user, launch.NETUnix, false, true, dateOnly)

	return utils.PrepareInputForMarkdown(text, "text")
}

// Constructs the message for a postpone notification
func (launch *Launch) PostponeNotificationMessage(postponedBy int64) (string, tb.SendOptions) {
	// New T- until launch
	untilLaunch := time.Until(time.Now().Add(time.Duration(postponedBy) * time.Second))

	// Text for the postpone notification
	text := fmt.Sprintf(
		"📣 *Postponed by %s:* %s\n\n"+
			"🕙 *New launch time*\n"+
			"*Date* $USERDATE\n"+
			"*Until* %s\n",

		durafmt.ParseShort(time.Duration(postponedBy)*time.Second),
		launch.HeaderName(),
		durafmt.ParseShort(untilLaunch),
	)

	muteBtn := tb.InlineButton{
		Unique: "muteToggle",
		Text:   "🔇 Mute launch",
		Data:   fmt.Sprintf("mute/%s/1/%s", launch.Id, "postpone"),
	}

	// Construct the keyboard
	kb := [][]tb.InlineButton{{muteBtn}}

	sendOptions := tb.SendOptions{
		ParseMode:   "MarkdownV2",
		ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
	}

	return text, sendOptions
}

// Builds a complete Sendable for a postpone notification
func (launch *Launch) PostponeNotificationSendable(db *Database, postpone Postpone, platform string) *sendables.Sendable {
	// Get text and send-options
	text, sendOptions := launch.PostponeNotificationMessage(postpone.PostponedBy)

	// Load recipients
	// TODO use reset states to get users who should be notified
	recipients := launch.NotificationRecipients(db, "postpone", platform)
	filteredRecipients := []*users.User{}

	// Iterate notification states in-order
	orderedStates := []string{"24h", "12h", "1h", "5min"}

	// Filter all recipients that have received one of the previous notifications
	for _, user := range recipients {
		// Load user's notification states
		userStates := user.NotificationTimePreferenceMap()

		// Loop over all states in order
		for _, state := range orderedStates {
			reset, ok := postpone.ResetStates[state]

			if !ok {
				log.Warn().Msgf("State=%s not found in resetStates (%#v)", state, postpone.ResetStates)
				continue
			}

			if reset {
				// If state was reset, check if user was subscribed to this type
				if userStates[state] == true {
					log.Debug().Msgf("User=%s had reset state=%s enabled, adding to postpone recipients", user.Id, state)
					filteredRecipients = append(filteredRecipients, user)
					break
				}
			}
		}
	}

	log.Debug().Msgf("Filtered postpone recipients from %d down to %d", len(recipients), len(filteredRecipients))

	sendable := sendables.Sendable{
		Type: "notification", NotificationType: "postpone", Platform: platform,
		LaunchId:   launch.Id,
		Recipients: filteredRecipients,
		Message: &sendables.Message{
			TextContent: text, AddUserTime: true, RefTime: launch.NETUnix, SendOptions: sendOptions,
		},
	}

	return &sendable
}

// Generate a launch name, either using the mission name or using a split launch name
func (launch *Launch) HeaderName() string {
	// Use the mission name; however, this may be empty
	name := launch.Mission.Name

	if name == "" {
		nameSplit := strings.Split(launch.Name, "|")

		if len(nameSplit) > 1 {
			name = strings.Trim(nameSplit[1], " ")
		} else {
			name = strings.Trim(nameSplit[0], " ")
		}
	}

	return name
}

//  Returns the first unsent notification type for a launch
func (launch *Launch) NextNotification(db *Database) Notification {
	// Not ordered, so we re-declare the list below
	NotificationSendTimes := map[string]time.Duration{
		"Sent24h":  time.Duration(24) * time.Hour,
		"Sent12h":  time.Duration(12) * time.Hour,
		"Sent1h":   time.Duration(1) * time.Hour,
		"Sent5min": time.Duration(5) * time.Minute,
	}

	// An ordered list of send times
	notificationClasses := []string{
		"Sent24h", "Sent12h", "Sent1h", "Sent5min",
	}

	// Minutes the send-time is allowed to slip by
	allowedSlipMins := 5

	// Update map before parsing
	launch.NotificationState = launch.NotificationState.UpdateMap()

	// Loop over the valid notification classes (24h, 12h, 1h, 5min)
	for _, notifType := range notificationClasses {
		// If notification of this type has not been sent, check when it should be sent
		if launch.NotificationState.Map[notifType] == false {
			// How many seconds before NET the notification is sent
			secBeforeNet, ok := NotificationSendTimes[notifType]

			if !ok {
				log.Error().Msgf("[launch.NextNotification] Error parsing notificationType for %s: %s",
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
					log.Warn().Msgf("Missed type=%s notification by %.2f minutes, id=%s; marking as sent...",
						notifType, missedBy.Minutes(), launch.Slug)

					// Launch was missed: log, and set as sent in database
					launch.NotificationState.Map[notifType] = true
					launch.NotificationState = launch.NotificationState.UpdateFlags()

					// Save state in db
					err := db.Update([]*Launch{launch}, false, false)
					if err != nil {
						log.Error().Err(err).Msg("Error saving updated notification states to disk")
					}

					continue
				} else {
					log.Info().Msgf("[launch.NextNotification] [%s] Missed type=%s by under %d min (%.2f min): modifying send-time",
						launch.Slug, notifType, allowedSlipMins, missedBy.Minutes())

					// Modify to send in 10 seconds
					sendTime = time.Now().Unix() + 10

					notifType = strings.ReplaceAll(notifType, "Sent", "")
					return Notification{Type: notifType, SendTime: sendTime,
						LaunchId: launch.Id, LaunchName: launch.Name, Count: 1}
				}
			}

			// Sent is false and has not been missed: return type
			notifType = strings.ReplaceAll(notifType, "Sent", "")
			return Notification{
				Type: notifType, SendTime: sendTime, LaunchId: launch.Id,
				LaunchName: launch.Name, Count: 1,
			}
		}
	}

	// No unsent notifications: return with AllSent=true
	return Notification{AllSent: true, LaunchId: launch.Id, LaunchName: launch.Name, Count: 0}
}

// Return a list of all provider IDs associated with a country-code
func AllIdsByCountryCode(cc string) []string {
	ids := []string{}
	for _, id := range IdByCountryCode[cc] {
		ids = append(ids, fmt.Sprint(id))
	}

	return ids
}