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

// FUTURE save in a database table + cache (V3.1)
// - when a new LSP ID is encountered in /launch/upcoming endpoint, request its info and insert into LSP table
// Currently contains all featured launch providers + a couple extra
// https://ll.thespacedevs.com/2.2.0/agencies/?featured=true&limit=50
var LSPShorthands = map[int]LSP{
	// Agencies
	17: {Name: "CNSA", Flag: "🇨🇳", Cc: "CHN"},
	31: {Name: "ISRO", Flag: "🇮🇳", Cc: "IND"},
	37: {Name: "JAXA", Flag: "🇯🇵", Cc: "JPN"},
	44: {Name: "NASA", Flag: "🇺🇸", Cc: "USA"},
	63: {Name: "ROSCOSMOS", Flag: "🇷🇺", Cc: "RUS"},

	// Corporations, including state and commercial
	96:  {Name: "KhSC", Flag: "🇷🇺", Cc: "RUS"},
	98:  {Name: "Mitsubishi H.I.", Flag: "🇯🇵", Cc: "JPN"},
	115: {Name: "Arianespace", Flag: "🇪🇺", Cc: "EU"},
	121: {Name: "SpaceX", Flag: "🇺🇸", Cc: "USA"},
	124: {Name: "ULA", Flag: "🇺🇸", Cc: "USA"},
	141: {Name: "Blue Origin", Flag: "🇺🇸", Cc: "USA"},
	147: {Name: "Rocket Lab", Flag: "🇺🇸", Cc: "USA"},
	189: {Name: "CASC", Flag: "🇨🇳", Cc: "CHN"},
	190: {Name: "Antrix Corp.", Flag: "🇮🇳", Cc: "IND"},
	194: {Name: "ExPace", Flag: "🇨🇳", Cc: "CHN"},
	199: {Name: "Virgin Orbit", Flag: "🇺🇸", Cc: "USA"},
	257: {Name: "Northrop Grumman", Flag: "🇺🇸", Cc: "USA"},
	259: {Name: "LandSpace", Flag: "🇨🇳", Cc: "CHN"},
	265: {Name: "Firefly", Flag: "🇺🇸", Cc: "USA"},
	274: {Name: "iSpace", Flag: "🇨🇳", Cc: "CHN"},
	285: {Name: "Astra", Flag: "🇺🇸", Cc: "USA"},

	// Small-scale providers, incl. sub-orbital operators
	1002: {Name: "Interstellar tech.", Flag: "🇯🇵", Cc: "JPN"},
	1021: {Name: "Galactic Energy", Flag: "🇨🇳", Cc: "CHN"},
	1024: {Name: "Virgin Galactic", Flag: "🇺🇸", Cc: "USA"},
	1029: {Name: "TiSPACE", Flag: "🇹🇼", Cc: "TWN"},
}

// Map country codes to a list of provider IDs under this country code
var IdByCountryCode = map[string][]int{
	"USA": {44, 121, 124, 141, 147, 199, 257, 265, 285, 1024},
	"EU":  {115},
	"CHN": {189, 194, 259, 274, 1021},
	"RUS": {63, 96},
	"IND": {31, 190},
	"JPN": {37, 98, 1002},
	"TWN": {1029},
}

// List of available countries (EU is effectively a faux-country)
var CountryCodes = []string{"USA", "EU", "CHN", "RUS", "IND", "JPN", "TWN"}

var CountryCodeToName = map[string]string{
	"USA": "USA 🇺🇸", "EU": "EU 🇪🇺", "CHN": "China 🇨🇳", "RUS": "Russia 🇷🇺",
	"IND": "India 🇮🇳", "JPN": "Japan 🇯🇵", "TWN": "Taiwan 🇹🇼",
}

type LSP struct {
	Name string
	Flag string
	Cc   string
}

type Notification struct {
	Type       string   // In (24h, 12h, 1h, 5min)
	SendTime   int64    // Unix-time of the notification
	AllSent    bool     // All notifications sent already?
	LaunchId   string   // Launch ID associated
	LaunchName string   // Name of launch
	Count      int      // If more than one, list their count
	IDs        []string // If more than one, include their IDs here
}

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

	// If mission has no name, use the name of the launch itself (and split by `|`)
	name := launch.HeaderName()

	// Shorten long LSP names
	providerName := launch.LaunchProvider.ShortName()

	// Get country-flag
	flag := ""
	if launch.LaunchProvider.CountryCode != "" {
		flag = " " + emoji.GetFlag(launch.LaunchProvider.CountryCode)
	}

	// TODO create a copy of the launch -> monospace all relevant fields?
	// TODO set bot username dynamically (throw a tb.bot object at ll2.notify?)

	// Mission information
	missionType := launch.Mission.Type
	missionOrbit := launch.Mission.Orbit.Name

	if missionType == "" {
		missionType = "Unknown purpose"
	}

	if missionOrbit == "" {
		missionOrbit = "Unknown orbit"
	}

	// If this is an expanded message, add a description
	launchDescription := ""
	if expanded {
		if launch.Mission.Description == "" {
			launchDescription = "ℹ️ No information available\n\n"
		} else {
			launchDescription = fmt.Sprintf("ℹ️ %s\n\n", launch.Mission.Description)
		}
	}

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

	// TODO add bot username dynamically
	text := fmt.Sprintf(
		"🚀 *%s: %s*\n"+
			"*Provider* %s%s\n"+
			"*Rocket* %s\n"+
			"*From* %s\n\n"+

			"🌍 *Mission information*\n"+
			"*Type* %s\n"+
			"*Orbit* %s\n\n"+

			"%s"+

			"LAUNCHLINKHERE"+
			"🕙 *Launch-time $USERTIME*\n"+
			"🔕 *Stop with /settings@tglaunchbot*",

		// Name, launching-in, provider, rocket, launch pad
		header, name,
		utils.Monospaced(providerName), flag, utils.Monospaced(launch.Rocket.Config.FullName),
		utils.Monospaced(launch.LaunchPad.Name),

		// Mission information
		utils.Monospaced(missionType), utils.Monospaced(missionOrbit),

		// Description, if expanded
		launchDescription,
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
	return utils.PrepareInputForMarkdown(message, "text")
}

// Creates the text content and Telegram reply keyboard for the /next command.
func (cache *Cache) LaunchListMessage(user *users.User, index int, returnKeyboard bool) (string, *tb.ReplyMarkup) {
	// Pull launch from cache at index
	launch := cache.Launches[index]

	// If cache has old launches, refresh it
	if launch.NETUnix < time.Now().Unix() {
		cache.Populate()
		launch = cache.Launches[index]
	}

	// If mission has no name, use the name of the launch itself (and split by `|`)
	name := launch.Mission.Name
	if name == "" {
		nameSplit := strings.Split(launch.Name, "|")

		if len(nameSplit) > 1 {
			name = strings.Trim(nameSplit[1], " ")
		} else {
			name = strings.Trim(nameSplit[0], " ")
		}
	}

	// Shorten long LSP names
	providerName := launch.LaunchProvider.ShortName()

	// Get country-flag for provider and location
	var (
		flag     string
		location string
	)

	if launch.LaunchProvider.CountryCode != "" {
		flag = fmt.Sprintf(" %s", emoji.GetFlag(launch.LaunchProvider.CountryCode))
	}

	if launch.LaunchPad.Location.CountryCode != "" {
		if launch.LaunchPad.Location.Name != "" {
			location = strings.Split(launch.LaunchPad.Location.Name, ",")[0] + " "
		}

		location = fmt.Sprintf(", %s%s", location, emoji.GetFlag(launch.LaunchPad.Location.CountryCode))
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

	description := ""
	if launch.Mission.Description != "" {
		description = fmt.Sprintf("ℹ️ %s\n\n", launch.Mission.Description)
	} else {
		description = ""
	}

	// Time until launch in a string format
	launchTime := time.Unix(launch.NETUnix, 0)

	nthDay := humanize.Ordinal(launchTime.Day())
	userTime := utils.TimeInUserLocation(launch.NETUnix, user.Time.Location, user.Time.UtcOffset)
	launchDate := fmt.Sprintf("%s %s, %s", launchTime.Month().String(), nthDay, userTime)

	timeUntil := fmt.Sprintf("T-%s", durafmt.Parse(time.Until(launchTime)).LimitFirstN(2))

	text := fmt.Sprintf(
		"🚀 *Next launch:* %s\n"+
			"*Provider* %s%s\n"+
			"*Rocket* %s\n"+
			"*From* %s%s\n\n"+

			"🕙 *Launch-time*\n"+
			"*Date* %s\n"+
			"*Until* %s\n\n"+

			"🌍 *Mission information*\n"+
			"*Type* %s\n"+
			"*Orbit* %s\n\n"+

			"%s",

		name,
		utils.Monospaced(providerName), flag, utils.Monospaced(launch.Rocket.Config.FullName),
		utils.Monospaced(launch.LaunchPad.Name), utils.Monospaced(location), utils.Monospaced(launchDate),
		utils.Monospaced(timeUntil), utils.Monospaced(missionType),
		utils.Monospaced(missionOrbit), description,
	)

	// Set user's time
	text = *sendables.SetTime(text, user, launch.NETUnix, false)

	if !returnKeyboard {
		return utils.PrepareInputForMarkdown(text, "text"), nil
	}

	// Create return kb
	var kb [][]tb.InlineButton

	switch index {
	case 0: // Case: first index
		refreshBtn := tb.InlineButton{
			Text: "Refresh 🔄", Data: "nxt/r/0",
		}

		nextBtn := tb.InlineButton{
			Text: "Next launch ➡️", Data: "nxt/n/1/+",
		}

		// Construct the keyboard
		kb = [][]tb.InlineButton{{nextBtn}, {refreshBtn}}
	case len(cache.Launches) - 1: // Case: last index
		refreshBtn := tb.InlineButton{
			Text: "Refresh 🔄", Data: fmt.Sprintf("nxt/r/%d", index),
		}

		returnBtn := tb.InlineButton{
			Text: "↩️ Back to first", Data: fmt.Sprintf("nxt/n/0/0"),
		}

		prevBtn := tb.InlineButton{
			Text: "⬅️ Previous launch", Data: fmt.Sprintf("nxt/n/%d/-", index-1),
		}

		// Construct the keyboard
		kb = [][]tb.InlineButton{{prevBtn}, {returnBtn, refreshBtn}}
	default: // Default case, i.e. not either end of the launch list
		if index > len(cache.Launches)-1 {
			// Make sure we don't go over the maximum index
			index = len(cache.Launches) - 1
		}

		refreshBtn := tb.InlineButton{
			Text: "Refresh 🔄", Data: fmt.Sprintf("nxt/r/%d", index),
		}

		returnBtn := tb.InlineButton{
			Text: "↩️ Back to first", Data: fmt.Sprintf("nxt/n/0/0"),
		}

		nextBtn := tb.InlineButton{
			Text: "Next launch ➡️", Data: fmt.Sprintf("nxt/n/%d/+", index+1),
		}

		prevBtn := tb.InlineButton{
			Text: "⬅️ Previous launch", Data: fmt.Sprintf("nxt/n/%d/-", index-1),
		}

		// Construct the keyboard
		kb = [][]tb.InlineButton{{prevBtn, nextBtn}, {returnBtn, refreshBtn}}
	}

	return utils.PrepareInputForMarkdown(text, "text"), &tb.ReplyMarkup{InlineKeyboard: kb}
}

// Extends the Launch struct to add a .PostponeNotify() method.
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

func (launch *Launch) PostponeNotificationSendable(db *Database, postponedBy int64, platform string) *sendables.Sendable {
	// Get text and send-options
	text, sendOptions := launch.PostponeNotificationMessage(postponedBy)

	// Load recipients
	recipients := launch.NotificationRecipients(db, "postpone", platform)

	sendable := sendables.Sendable{
		Type: "notification", NotificationType: "postpone", Platform: platform,
		LaunchId:   launch.Id,
		Recipients: recipients,
		Message: &sendables.Message{
			TextContent: &text, AddUserTime: true, RefTime: launch.NETUnix, SendOptions: sendOptions,
		},
	}

	return &sendable
}

func (launch *Launch) HeaderName() string {
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

// Extend the LaunchProvider type to get a short name, if one exists
func (provider *LaunchProvider) ShortName() string {
	// Check if a short name exists
	_, ok := LSPShorthands[provider.Id]

	if ok {
		return LSPShorthands[provider.Id].Name
	}

	// Log long names we encounter
	if len(provider.Name) > len("Virgin Orbit") {
		log.Warn().Msgf("Provider name '%s' not found in LSPShorthands, id=%d (not warning again)",
			provider.Name, provider.Id)

		return provider.Abbrev
	}

	return provider.Name
}

// Updates the notification-state map from the boolean values
func (state *NotificationState) UpdateMap() NotificationState {
	state.Map = map[string]bool{
		"Sent24h":  state.Sent24h,
		"Sent12h":  state.Sent12h,
		"Sent1h":   state.Sent1h,
		"Sent5min": state.Sent5min,
	}

	return *state
}

// Updates the notification-state booleans from the map
func (state *NotificationState) UpdateFlags() NotificationState {
	// Update booleans
	state.Sent24h = state.Map["Sent24h"]
	state.Sent12h = state.Map["Sent12h"]
	state.Sent1h = state.Map["Sent1h"]
	state.Sent5min = state.Map["Sent5min"]
	return *state
}

// Return a boolean, indicating whether any notifications have been sent for this launch
func (state *NotificationState) AnyNotificationsSent() bool {
	for _, state := range state.Map {
		if state {
			return true
		}
	}

	return false
}

// Functions checks if a NET slip of $slip seconds resets any notification send states
func (launch *Launch) AnyStatesResetByNetSlip(slip int64) bool {
	// Map notification types to seconds
	notificationTypePreSendTime := map[string]int64{
		"24h": 24 * 3600, "12h": 12 * 3600, "1h": 3600, "5min": 5 * 60,
	}

	// Only do a disk op if a state was altered
	stateAltered := false

	// Loop over current notification send states
	for notification, sent := range launch.NotificationState.Map {
		if !sent {
			continue
		}

		// Get time this notification window ends at
		windowEndTime := launch.NETUnix - notificationTypePreSendTime[notification]

		// Time until window end, plus NET slip
		windowDelta := windowEndTime - time.Now().Unix() + slip

		// Check if the NET slip puts us back before this notification window
		if windowEndTime > time.Now().Unix()-slip {
			// Launch was postponed: flip the notification state
			launch.NotificationState.Map[notification] = false
			stateAltered = true

			log.Debug().Msgf("Launch had its notification state reset with delta of %s (type=%s, launch=%s)",
				durafmt.ParseShort(time.Duration(windowDelta)*time.Second), notification, launch.Id)
		}
	}

	// Save updated states
	if stateAltered {
		launch.NotificationState.UpdateFlags()
		return true
	}

	return false
}

// Gets all notification recipients for this notification
func (launch *Launch) NotificationRecipients(db *Database, notificationType string, platform string) []*users.User {
	usersWithNotificationEnabled := []*users.User{}

	// Map notification type to a database table name
	tableNotifType := ""

	if notificationType == "postpone" {
		tableNotifType = "enabled_postpone"
	} else {
		tableNotifType = fmt.Sprintf("enabled%s", notificationType)
	}

	// Get all chats that have this notification type enabled
	result := db.Conn.Model(&users.User{}).Where(
		fmt.Sprintf("%s = ? AND platform = ?", tableNotifType), 1, platform,
	).Find(&usersWithNotificationEnabled)

	if result.Error != nil {
		log.Error().Err(result.Error).Msg("Error loading notification recipient list")
	}

	// List of final recipients
	recipients := []*users.User{}

	// Filter all users from the list
	for _, user := range usersWithNotificationEnabled {
		// Check if user is subscribed to this provider
		if !user.GetNotificationStatusById(launch.LaunchProvider.Id) {
			log.Debug().Msgf("User=%s is not subscribed to provider with id=%d", user.Id, launch.LaunchProvider.Id)
			continue
		}

		// Check if user has this specific launch muted
		if user.HasMutedLaunch(launch.Id) {
			log.Debug().Msgf("User=%s has muted launch with id=%s", user.Id, launch.Id)
			continue
		}

		// User has subscribed to this launch, and has not muted it: add to recipients
		log.Debug().Msgf("Adding user=%s to recipients", user.Id)
		recipients = append(recipients, user)
	}

	return recipients
}

// Load IDs into a map
func (launch *Launch) LoadSentNotificationIdMap() map[string]string {
	sentIds := map[string]string{}

	// A comma-separated slice of chat_id:msg_id strings
	for _, idPair := range strings.Split(launch.SentNotificationIds, ",") {
		// User id (0), message id (1)
		ids := strings.Split(idPair, ":")
		sentIds[ids[0]] = ids[1]
	}

	return sentIds
}

func (launch *Launch) SaveSentNotificationIds(ids []string, db *Database) {
	// Join the IDs
	launch.SentNotificationIds = strings.Join(ids, ",")

	// Insert the launch back into the database
	err := db.Update([]*Launch{launch}, false, false)

	if err != nil {
		log.Error().Err(err).Msgf("Saving notification message IDs to disk failed")
	} else {
		log.Debug().Msgf("Sent notification message IDs saved")
	}
}
