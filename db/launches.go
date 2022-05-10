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

// TODO save in a database table + cache (V3.1)
// TODO: add iSpace, LandSpace
var LSPShorthands = map[int]LSP{
	31:   {Name: "ISRO", Flag: "🇮🇳", Cc: "IND"},
	37:   {Name: "JAXA", Flag: "🇯🇵", Cc: "JPN"},
	44:   {Name: "NASA", Flag: "🇺🇸", Cc: "USA"},
	63:   {Name: "ROSCOSMOS", Flag: "🇷🇺", Cc: "RUS"},
	96:   {Name: "KhSC", Flag: "🇷🇺", Cc: "RUS"},
	98:   {Name: "Mitsubishi HI", Flag: "🇯🇵", Cc: "JPN"},
	99:   {Name: "Northrop Grumman", Flag: "🇺🇸", Cc: "USA"},
	115:  {Name: "Arianespace", Flag: "🇪🇺", Cc: "EU"},
	121:  {Name: "SpaceX", Flag: "🇺🇸", Cc: "USA"},
	124:  {Name: "ULA", Flag: "🇺🇸", Cc: "USA"},
	141:  {Name: "Blue Origin", Flag: "🇺🇸", Cc: "USA"},
	147:  {Name: "Rocket Lab", Flag: "🇺🇸", Cc: "USA"},
	189:  {Name: "CASC", Flag: "🇨🇳", Cc: "CHN"},
	190:  {Name: "Antrix Corp.", Flag: "🇮🇳", Cc: "IND"},
	194:  {Name: "ExPace", Flag: "🇨🇳", Cc: "CHN"},
	199:  {Name: "Virgin Orbit", Flag: "🇺🇸", Cc: "USA"},
	265:  {Name: "Firefly", Flag: "🇺🇸", Cc: "USA"},
	285:  {Name: "Astra", Flag: "🇺🇸", Cc: "USA"},
	1002: {Name: "Interstellar tech.", Flag: "🇯🇵", Cc: "JPN"},
	1021: {Name: "Galactic Energy", Flag: "🇨🇳", Cc: "CHN"},
	1024: {Name: "Virgin Galactic", Flag: "🇺🇸", Cc: "USA"},
	1029: {Name: "TiSPACE", Flag: "🇹🇼", Cc: "TWN"},
}

// TODO add ispace, landspace
var IdByCountryCode = map[string][]int{
	"USA": {44, 99, 121, 124, 141, 147, 199, 265, 285, 1024},
	"EU":  {115},
	"CHN": {189, 194, 1021},
	"RUS": {63, 96},
	"IND": {31, 190},
	"JPN": {37, 98, 1002},
	"TWN": {1029},
}

var CountryCodes = []string{"USA", "EU", "CHN", "RUS", "IND", "JPN", "TWN"}
var CountryCodeToName = map[string]string{
	"USA": "USA 🇺🇸", "EU": "EU 🇪🇺", "CHN": "China 🇨🇳", "RUS": "Russia 🇷🇺", "IND": "India 🇮🇳", "JPN": "Japan 🇯🇵", "TWN": "Taiwan 🇹🇼",
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
	Launched    bool      // If success/failure/partial_failure
	WebcastLink string    // Highest-priority link from VidURLs
	ApiUpdate   time.Time // Last API update

	// Status of notification sends (boolean + non-embedded map)
	NotificationState NotificationState `gorm:"embedded"`

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
	// TODO use ID to find more info from API -> store in DB -> re-use
	Id     int    `json:"id"`
	Name   string `json:"name"`
	Abbrev string `json:"abbrev"`
	Type   string `json:"type"`
	URL    string `json:"url"`

	// TODO Rarely given: manually parse from the URL endpoint given -> save
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
		"24h":  "in 24 hours",
		"12h":  "in 12 hours",
		"1h":   "in 60 minutes",
		"5min": "in 5 minutes",
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
				header = "now"
			} else {
				header = fmt.Sprintf("in %d seconds", int(untilNet.Seconds()))
			}
		} else {
			header = fmt.Sprintf("in %d minutes", int(untilNet.Minutes()))
		}
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
			linkText := utils.PrepareInputForMarkdown("🔴 Watch launch live!", "text")
			webcastLink = fmt.Sprintf("[*%s*](%s)\n", linkText, link)
		} else {
			// No video available
			webcastLink = "🔇 *No live video available*\n"
		}
	} else {
		webcastLink = ""
	}

	text := fmt.Sprintf(
		"🚀 *%s is launching %s*\n"+
			"*Provider* %s%s\n"+
			"*Rocket* %s\n"+
			"*From* %s\n\n"+

			"🌍 *Mission information*\n"+
			"*Type* %s\n"+
			"*Orbit* %s\n\n"+

			"%s"+

			"🕑 *Lift-off at $USERTIME*\n"+
			"LAUNCHLINKHERE"+
			"🔕 *Stop with /notify@tglaunchbot*",

		// Name, launching-in, provider, rocket, launch pad
		name, header,
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
// TODO simplify, now that launch cache is truly ordered (do in one loop)
func (cache *Cache) ScheduleMessage(user *users.User, showMissions bool) string {
	// List of launch-lists, one per launch date
	schedule := [][]*Launch{}

	// Maps in Go don't preserve order on iteration: stash the indices of each date
	dateToIndex := make(map[string]int)

	// The launch cache is always ordered (sorted when API update is parsed)
	freeIdx := 0
	for _, launch := range cache.Launches {
		// Ignore bad launches (only really with LL2's development endpoint)
		delta := time.Until(time.Unix(launch.NETUnix, 0))
		if delta.Seconds() < 0 && launch.Status.Abbrev != "In Flight" {
			continue
		}

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
			schedule = append(schedule, []*Launch{launch})

			// Keep track of the index we added the launch to
			dateToIndex[dateStr] = freeIdx
			freeIdx++
		}
	}

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
		fmt.Sprintf("_Dates are relative to %s. ", user.Time.UtcOffset) +
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
			var daysUntil float64

			// Remove seconds that are not contributing to a whole day.
			// As in, TTL might be 1.25 days: extract the .25 days
			mod := int64(timeToLaunch.Seconds()) % (24 * 3600)

			// If, even after adding the remainder, the day is still today, calculating days is simple
			if time.Now().Add(time.Second*time.Duration(mod)).Day() == time.Now().Day() {
				daysUntil = timeToLaunch.Hours() / 24
			} else {
				// If the remained, e.g. .25 days, causes us to jump over to tomorrow, add a +1 to the days
				daysUntil = timeToLaunch.Hours()/24 + 1
			}

			if daysUntil < 2.0 {
				// The case of the date being today has already been caught, therefore it's tomorrow
				etaString = "tomorrow"
			} else {
				// Otherwise, just count the days
				etaString = fmt.Sprintf("in %s", english.Plural(int(daysUntil), "day", "days"))
			}
		}

		// The header of the date, e.g. "June 1st in 7 days"
		header := fmt.Sprintf("*%s %s* %s\n",
			userLaunchTime.Month().String(), humanize.Ordinal(userLaunchTime.Day()), etaString,
		)

		// Add header
		msg += header

		// Loop over launches, add
		var row string
		for i, launch := range launchList {
			if i == 3 {
				msg += fmt.Sprintf("*+ %d more %s*\n", len(launchList)-i, english.Plural(int(len(launchList)-i), "flight", "flights"))
				break
			}

			if !showMissions {
				// Create the row (vehicle-mode)
				row = fmt.Sprintf("%s%s %s %s", utils.Monospaced(statusToIndicator[launch.Status.Abbrev]), emoji.GetFlag(launch.LaunchProvider.CountryCode),
					utils.Monospaced(launch.LaunchProvider.ShortName()),
					utils.Monospaced(launch.Rocket.Config.Name))
			} else {
				// Create the row (mission-mode)
				missionName := launch.Mission.Name
				if missionName == "" {
					missionName = "Unknown payload"
				}

				// Flag, status, mission name
				row = fmt.Sprintf("%s%s %s",
					utils.Monospaced(statusToIndicator[launch.Status.Abbrev]), emoji.GetFlag(launch.LaunchProvider.CountryCode), utils.Monospaced(missionName))
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

	// Escape the message, return it
	msg = utils.PrepareInputForMarkdown(msg, "text")
	return msg
}

// Creates the text content and Telegram reply keyboard for the /next command.
func (cache *Cache) LaunchListMessage(user *users.User, index int, returnKeyboard bool) (string, *tb.ReplyMarkup) {
	// Pull launch from cache at index
	launch := cache.Launches[index]

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
		utils.Monospaced(providerName), flag,
		utils.Monospaced(launch.Rocket.Config.FullName),
		utils.Monospaced(launch.LaunchPad.Name), utils.Monospaced(location),
		utils.Monospaced(launchDate),
		utils.Monospaced(timeUntil),
		utils.Monospaced(missionType),
		utils.Monospaced(missionOrbit),
		description,
	)

	// Set user's time
	text = *sendables.SetTime(text, user, launch.NETUnix, false)

	if !returnKeyboard {
		return utils.PrepareInputForMarkdown(text, "text"), nil
	}

	// Create return kb
	var kb [][]tb.InlineButton

	switch index {
	case 0:
		// Case: first index
		refreshBtn := tb.InlineButton{
			Text: "Refresh 🔄",
			Data: "nxt/r/0",
		}

		nextBtn := tb.InlineButton{
			Text: "Next launch ➡️",
			Data: "nxt/n/1/+",
		}

		// Construct the keyboard
		kb = [][]tb.InlineButton{{nextBtn}, {refreshBtn}}
	case len(cache.Launches) - 1:
		// Case: last index
		refreshBtn := tb.InlineButton{
			Text: "Refresh 🔄",
			Data: fmt.Sprintf("nxt/r/%d", index),
		}

		returnBtn := tb.InlineButton{
			Text: "↩️ Back to first",
			Data: fmt.Sprintf("nxt/n/0/0"),
		}

		prevBtn := tb.InlineButton{
			Text: "⬅️ Previous launch",
			Data: fmt.Sprintf("nxt/n/%d/-", index-1),
		}

		// Construct the keyboard
		kb = [][]tb.InlineButton{{prevBtn}, {returnBtn, refreshBtn}}
	default:
		// Default case, i.e. not either end of the launch list
		if index > len(cache.Launches)-1 {
			// Make sure we don't go over the maximum index
			index = len(cache.Launches) - 1
		}

		refreshBtn := tb.InlineButton{
			Text: "Refresh 🔄",
			Data: fmt.Sprintf("nxt/r/%d", index),
		}

		returnBtn := tb.InlineButton{
			Text: "↩️ Back to first",
			Data: fmt.Sprintf("nxt/n/0/0"),
		}

		nextBtn := tb.InlineButton{
			Text: "Next launch ➡️",
			Data: fmt.Sprintf("nxt/n/%d/+", index+1),
		}

		prevBtn := tb.InlineButton{
			Text: "⬅️ Previous launch",
			Data: fmt.Sprintf("nxt/n/%d/-", index-1),
		}

		// Construct the keyboard
		kb = [][]tb.InlineButton{{prevBtn, nextBtn}, {returnBtn, refreshBtn}}
	}

	return utils.PrepareInputForMarkdown(text, "text"), &tb.ReplyMarkup{InlineKeyboard: kb}
}

// Extends the Launch struct to add a .PostponeNotify() method.
func (launch *Launch) PostponeNotify(postponedTo int) {
}

// Pulls recipients for this notification type from the DB
func (launch *Launch) GetRecipients(db *Database, notifType *Notification) *users.UserList {
	// TODO Implement
	recipients := users.UserList{Platform: "tg", Users: []*users.User{}}
	user := users.User{Platform: recipients.Platform, Id: fmt.Sprint(db.Owner)}

	recipients.Add(&user, true)

	return &recipients
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
