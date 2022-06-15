package db

import (
	"fmt"
	"launchbot/sendables"
	"launchbot/users"
	"launchbot/utils"
	"math"
	"strings"
	"sync"
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
	Launches  []*Launch            `json:"results"`
	Postponed map[*Launch]Postpone // Map of postponed launches
	Mutex     sync.Mutex           // A mutex for concurrently parsing launches
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

	// Unparsed and parsed launcher info: JSON is unpacked into the unparsed struct
	UnparsedLauncherInfo []FirstStage `json:"launcher_stage" gorm:"-:all"`
	Launchers            Launchers    `gorm:"embedded;embeddedPrefix:launcher_"`

	// TODO
	// Spacecraft SpacecraftStage     `json:"spacecraft_stage"`
}

// Contains multiple launchers packed into one (e.g. Falcon 9 vs. Falcon Heavy).
// A launcher is e.g. a Falcon 9 first stage.
type Launchers struct {
	Count    int
	Core     Launcher `gorm:"embedded;embeddedPrefix:core_"`
	Boosters Launcher `gorm:"embedded;embeddedPrefix:boosters_"` // Comma-separated values
}

// A parsed launcher info struct
type Launcher struct {
	Serial          string
	Reused          bool
	FlightNumber    int
	Flights         int
	FirstLaunchDate string
	LastLaunchData  string
	LandingAttempt  bool
	LandingSuccess  bool
	LandingLocation LandingLocation `gorm:"embedded;embeddedPrefix:landing_location_"`
	LandingType     LandingType     `gorm:"embedded;embeddedPrefix:landing_type_"`
}

type RocketConfiguration struct {
	Id       int    `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Variant  string `json:"variant"`

	TotalLaunchCount int `json:"total_launch_count"`
}

// Unparsed information straight from the API: parsed later into one struct
type FirstStage struct {
	Type         string           `json:"type"`
	Reused       bool             `json:"reused"`
	FlightNumber int              `json:"launcher_flight_number"`
	Detailed     LauncherDetailed `json:"launcher"`
	Landing      Landing          `json:"landing"`
}

// More unparsed information
type LauncherDetailed struct {
	FlightProven    bool   `json:"flight_proven"`
	Serial          string `json:"serial_number"`
	Flights         int    `json:"flights"`
	LastLaunchDate  string `json:"last_launch_date"`
	FirstLaunchDate string `json:"first_launch_date"`
}

type Landing struct {
	Attempt  bool            `json:"attempt"`
	Success  bool            `json:"success"`
	Location LandingLocation `json:"location"`
	Type     LandingType     `json:"type"`
}

type LandingLocation struct {
	Name     string   `json:"name"`
	Abbrev   string   `json:"abbrev"`
	Location Location `json:"location" gorm:"embedded;embeddedPrefix:location_"`
}

type Location struct {
	Name        string `json:"name"`
	CountryCode string `json:"country_code"`
}

type LandingType struct {
	Name        string `json:"name"`
	Abbrev      string `json:"abbrev"`
	Description string `json:"description"`
}

// type SpacecraftStage struct {}

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

// Map isReused to an emoji indicating re-use status
var reuseIcon = map[bool]string{
	true: "♻️", false: "🌟",
}

var landingLocIcon = map[string]string{
	"ATL": " 🌊", "PAC": " 🌊", "CR": " 🤠",
}

var landingLocName = map[string]string{
	"CR": "Corn Ranch", "ATL": "Atlantic Ocean", "PAC": "Pacific Ocean",
	"N/A": "Unknown",
}

func (launch *Launch) BoosterInformation() string {
	var (
		boosterText         string
		landingLocationName string
		landingString       string
		reuseSymbol         string
	)

	if launch.Rocket.Launchers.Count == 1 {
		var boosterNamePrefix string
		core := &launch.Rocket.Launchers.Core

		if launch.LaunchProvider.Name == "SpaceX" && !strings.Contains(core.Serial, "Unknown F9") {
			// For SpaceX launches, add a booster prefix (e.g. B1060.1)
			boosterNamePrefix = fmt.Sprintf(".%d", core.FlightNumber)
		}

		// Get a nice icon for the landing type
		landingIcon, ok := landingLocIcon[core.LandingLocation.Abbrev]

		if !ok {
			if core.LandingType.Abbrev == "ASDS" {
				landingIcon = " 🌊"
			}
		}

		// Check if there's a friendlier name for the landing location
		landingLocationName, ok = landingLocName[core.LandingLocation.Abbrev]

		if !ok {
			landingLocationName = core.LandingLocation.Abbrev
		}

		// Symbol to indicate reuse: also check for potentially errenous data
		reuseSymbol = reuseIcon[core.Reused]

		if core.Reused == false && core.FlightNumber > 1 {
			reuseSymbol = "♻️"
		}

		// E.g. (7th flight ♻️)
		flightCountString := fmt.Sprintf("(%s flight %s)", humanize.Ordinal(core.FlightNumber), reuseSymbol)

		// E.g. (Pacific Ocean 🌊)
		landingNameString := fmt.Sprintf("(%s%s)", core.LandingType.Abbrev, landingIcon)

		if core.LandingType.Abbrev == "" {
			// Unknown landing type, avoid an empty string
			landingString = "Unknown"
		} else if core.LandingAttempt == false {
			// No landing attempt: core being expended
			landingString = "Expend 💥"
		} else {
			// All good: build a string
			landingString = fmt.Sprintf("%s %s", landingLocationName, landingNameString)
		}

		boosterText = fmt.Sprintf(
			"🚀 *Booster information*\n"+
				"*Core* %s %s\n"+
				"*Landing* %s\n\n",

			// Core
			utils.Monospaced(core.Serial+boosterNamePrefix), utils.Monospaced(flightCountString),

			// Landing
			utils.Monospaced(landingString),
		)
	}

	return boosterText
}

func (launch *Launch) DescriptionText() string {
	var description string

	if strings.TrimSpace(launch.Mission.Description) == "" {
		description = "ℹ️ No information available\n\n"
	} else {
		description = fmt.Sprintf("ℹ️ %s\n\n", launch.Mission.Description)
	}

	return description
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

	var timeUntil string
	untilLaunch := time.Until(time.Unix(launch.NETUnix, 0))

	if !isNotification {
		// If not a notification, add the "Launch time" section with date and time
		if launch.Status.Abbrev == "TBD" {
			// If launch-time is still TBD, add a Not-earlier-than date and reduce time accuracy
			timeUntil = fmt.Sprintf("%s", durafmt.Parse(untilLaunch).LimitFirstN(2))

			launchTimeSection = fmt.Sprintf(
				"🕙 *Launch time*\n"+
					"*No earlier than* $USERDATE\n"+
					"*Until launch* %s\n\n",

				utils.Monospaced(timeUntil),
			)
		} else {
			// Otherwise, the date is close enough
			if untilLaunch.Seconds() >= 60.0 {
				timeUntil = fmt.Sprintf("%s", durafmt.Parse(untilLaunch).LimitFirstN(2))
			} else {
				// We don't need millisecond-precision for the launch time
				timeUntil = fmt.Sprintf("%s", durafmt.Parse(untilLaunch).LimitFirstN(1))
			}

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
		// Add re-use information, if it exists
		if launch.Rocket.Launchers.Count != 0 {
			description = launch.BoosterInformation()
		}

		description += launch.DescriptionText()
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
func (launch *Launch) NotificationMessage(notifType string, expanded bool, botUsername string) string {
	// Map notification type to a header
	header, ok := map[string]string{
		"24h": "T-24 hours", "12h": "T-12 hours",
		"1h": "T-60 minutes", "5min": "T-5 minutes",
	}[notifType]

	if !ok {
		log.Warn().Msgf("%s not found when mapping notif.Type to header in NotificationMessage (%s)",
			notifType, launch.Slug)
	}

	// If notification type is 5-minute, use real launch time for clarity
	if notifType == "5min" && !expanded {
		untilNet := time.Until(time.Unix(launch.NETUnix, 0))

		// If we're seconds away, use seconds
		if untilNet.Minutes() < 1.0 {
			log.Warn().Msgf("Launch less than 1 minute away (seconds=%.1f), formatting as seconds",
				untilNet.Seconds())

			// If less than 0 seconds, set to "now"
			if untilNet.Seconds() < 0 {
				header = "Launching now"
			} else {
				header = fmt.Sprintf("T-%d %s",
					int(untilNet.Seconds()), english.PluralWord(int(untilNet.Minutes()), "second", ""))
			}
		} else {
			// Otherwise, use actual minutes
			header = fmt.Sprintf("T-%d %s",
				int(untilNet.Minutes()), english.PluralWord(int(untilNet.Minutes()), "minute", ""))
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

	text := fmt.Sprintf(
		"🚀 *%s*: *%s*\n"+
			"%s"+

			"🕙 *$USERDATE*\n"+
			"LAUNCHLINKHERE"+
			"🔕 *Stop with /settings@%s*",

		// Name, launching-in, provider, rocket, launch pad
		header, utils.Monospaced(name), messageBody, botUsername,
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

func (launch *Launch) TelegramNotificationKeyboard(notificationType string) [][]tb.InlineButton {
	// Construct the keeb
	kb := [][]tb.InlineButton{}

	// Notification is only sent to users that don't have the launch muted
	muteBtn := tb.InlineButton{
		Unique: "muteToggle",
		Text:   "🔇 Mute launch",
		Data:   fmt.Sprintf("%s/1/%s", launch.Id, notificationType),
	}

	kb = append(kb, []tb.InlineButton{muteBtn})

	if launch.Mission.Description != "" {
		expandBtn := tb.InlineButton{
			Unique: "expand",
			Text:   "ℹ️ Expand description",
			Data:   fmt.Sprintf("%s/%s", launch.Id, notificationType),
		}

		kb = append(kb, []tb.InlineButton{expandBtn})
	}

	return kb
}

// Creates a schedule message from the launch cache
func (cache *Cache) ScheduleMessage(user *users.User, showMissions bool, botUsername string) string {
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
	message := "📅 *5-day flight schedule*\n" +
		fmt.Sprintf("_Dates are relative to %s. ", user.Time.UtcOffset) +
		fmt.Sprintf("For detailed flight information, use /next@%s._\n\n", botUsername)

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

// Load the launch at index=i from launches the user has subscribed to.
// Returns the launch at index, length of cache, and if user is subscribed to this launch (does not indicate notification states)
func (cache *Cache) LaunchUserHasSubscribedToAtIndex(user *users.User, index int) (*Launch, int, bool) {
	if user.SubscribedAll && user.UnsubscribedFrom == "" {
		// If user has subscribed to all launches, and unsubscribed from none
		return cache.Launches[index], len(cache.Launches), true
	}

	if user.SubscribedTo == "" && user.UnsubscribedFrom == "" {
		// user has subscribed to nothing, and unsubscribed from nothing
		return cache.Launches[index], len(cache.Launches), false
	}

	// Load user states
	userNotifStates := user.GetNotificationStateMap()

	// Track what launches user has subscribed to
	subscribedTo := []*Launch{}

	// If user has all enabled notifications, verify this ID is not in disabled IDs
	var (
		ok     bool
		status bool
	)

	for _, launch := range cache.Launches {
		// Load launch from notification states
		status, ok = userNotifStates[launch.LaunchProvider.Id]

		if user.SubscribedAll && !ok {
			// If user has subscribed to all, and launch is not found, then it has not been disabled
			subscribedTo = append(subscribedTo, launch)
		} else if !user.SubscribedAll && status == true {
			// If user has not subscribed to all launches and state is enabled, add
			subscribedTo = append(subscribedTo, launch)
		}
	}

	if len(subscribedTo) == 0 {
		// If zero launches that user has subscribed to are coming up, show all launches
		// FUTURE make the first message a "No launches coming up with your settings..."
		return cache.Launches[index], len(cache.Launches), false
	}

	if index >= len(subscribedTo) {
		log.Warn().Msgf("Index=%d greater than length of subscribedTo (=%d)", index, len(subscribedTo))
		return subscribedTo[len(subscribedTo)-1], len(subscribedTo), true
	}

	return subscribedTo[index], len(subscribedTo), true
}

// Creates the text content and Telegram reply keyboard for the /next command.
func (cache *Cache) NextLaunchMessage(user *users.User, index int) (string, int) {
	// Ensure index doesn't go over the max index
	if index >= len(cache.Launches) {
		index = len(cache.Launches) - 1
	}

	// Pull launch from cache at index
	launch, userSubLaunchCount, subscribedTo := cache.LaunchUserHasSubscribedToAtIndex(user, index)

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

		utils.Monospaced(name),
		launch.MessageBodyText(true, false),
	)

	if subscribedTo && user.AnyNotificationTimesEnabled() {
		// Check if user has muted this launch, or has any notifications at all enabled
		if !user.HasMutedLaunch(launch.Id) {
			text += "🔔 You are subscribed to this launch"
		} else {
			text += "🔇 You have muted this launch"
		}
	} else {
		if !user.AnyNotificationTimesEnabled() {
			// If user has not enabled any notifications
			text += "🔕 You have disabled all notifications"
		} else {
			// If user is not subscribed to this launch
			text += "🔕 You are not subscribed to this launch"
		}
	}

	// Check if the launch date is TBD, and we should use the low-accuracy NET date
	dateOnly := false
	if launch.Status.Abbrev == "TBD" {
		dateOnly = true
	}

	// Set user's time
	text = sendables.SetTime(text, user, launch.NETUnix, false, true, dateOnly)

	return utils.PrepareInputForMarkdown(text, "text"), userSubLaunchCount
}

// Constructs the message for a postpone notification
func (launch *Launch) PostponeNotificationMessage(postponedBy int64) (string, tb.SendOptions) {
	// New T- until launch
	untilLaunch := time.Until(time.Unix(launch.NETUnix, 0))
	log.Debug().Msgf("Generating postpone message, postponedBy=%d", postponedBy)

	// Text for the postpone notification
	text := fmt.Sprintf(
		"📢 *%s %s* has been postponed by %s. Next launch attempt in %s.\n\n"+

			"📅 Launch date $USERDATE\n"+
			"ℹ️ _You will be re-notified of this launch._",

		launch.LaunchProvider.ShortName(),
		launch.HeaderName(),

		durafmt.Parse(time.Second*time.Duration(postponedBy)).LimitFirstN(2).String(),
		durafmt.Parse(untilLaunch).LimitFirstN(2).String(),
	)

	text = utils.PrepareInputForMarkdown(text, "italictext")

	muteBtn := tb.InlineButton{
		Unique: "muteToggle",
		Text:   "🔇 Mute launch",
		Data:   fmt.Sprintf("%s/1/%s", launch.Id, "postpone"),
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

	log.Debug().Msgf("Text generated:\n%s", text)

	// Load recipients
	// TODO use reset states to get users who should be notified
	recipients := launch.NotificationRecipients(db, "postpone", platform)
	filteredRecipients := []*users.User{}

	// Iterate notification states in-order
	orderedStates := []string{"Sent24h", "Sent12h", "Sent1h", "Sent5min"}

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
				if userStates[strings.ReplaceAll(state, "Sent", "")] {
					log.Debug().Msgf("User=%s has enabled reset_state=%s, adding to recipients", user.Id, state)
					filteredRecipients = append(filteredRecipients, user)
					break
				} else {
					log.Debug().Msgf("User=%s has disabled reset_state=%s, skipping this state", user.Id, state)
				}
			}
		}
	}

	log.Debug().Msgf("Filtered postpone recipients: %d ➙ %d", len(recipients), len(filteredRecipients))

	sendable := sendables.Sendable{
		Type:             sendables.Notification,
		NotificationType: "postpone",
		Platform:         platform,
		LaunchId:         launch.Id,
		Recipients:       filteredRecipients,
		Message: &sendables.Message{
			TextContent: text,
			AddUserTime: true,
			RefTime:     launch.NETUnix,
			SendOptions: sendOptions,
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

	// Configure time to pre-send notifications by
	preSendBy := time.Duration(1) * time.Minute

	// Minutes the send-time is allowed to slip by
	allowedSlip := time.Duration(5)*time.Minute + preSendBy

	// Update map before parsing
	launch.NotificationState.UpdateMap(launch)

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

			// Calculate send-time from NET, and deduct the pre-send time
			sendTime := launch.NETUnix - int64(secBeforeNet.Seconds()) - int64(preSendBy.Seconds())

			if sendTime-time.Now().Unix() < 0 {
				// Calculate how many minutes the notification was missed by
				missedBy := time.Duration(math.Abs(float64(time.Now().Unix()-sendTime))) * time.Second

				// TODO implement launch.ClearMissedNotifications + database update
				if missedBy > allowedSlip {
					log.Warn().Msgf("Missed type=%s notification by %.2f minutes, id=%s; marking as sent...",
						notifType, missedBy.Minutes(), launch.Slug)

					// Launch was missed: log, and set as sent in database
					launch.NotificationState.Map[notifType] = true
					launch.NotificationState.UpdateFlags(launch)

					// Save state in db
					err := db.Update([]*Launch{launch}, false, false)
					if err != nil {
						log.Error().Err(err).Msg("Error saving updated notification states to disk")
					}

					continue
				} else {
					log.Info().Msgf("[launch.NextNotification] [%s] Missed type=%s by under %.1f min (%.2f min): modifying send-time",
						launch.Slug, notifType, allowedSlip.Minutes(), missedBy.Minutes())

					// Modify to send in 10 seconds
					sendTime = time.Now().Unix() + 10

					notifType = strings.ReplaceAll(notifType, "Sent", "")

					return Notification{
						Type: notifType, SendTime: sendTime, LaunchId: launch.Id,
						LaunchName: launch.Name, LaunchNET: launch.NETUnix, Count: 1}
				}
			}

			// Sent is false and has not been missed: return type
			notifType = strings.ReplaceAll(notifType, "Sent", "")

			return Notification{
				Type: notifType, SendTime: sendTime, LaunchId: launch.Id,
				LaunchName: launch.Name, LaunchNET: launch.NETUnix, Count: 1,
			}
		}
	}

	// No unsent notifications: return with AllSent=true
	return Notification{
		AllSent: true, LaunchId: launch.Id, LaunchName: launch.Name,
		LaunchNET: launch.NETUnix, Count: 0}
}

// Return a list of all provider IDs associated with a country-code
func AllIdsByCountryCode(cc string) []string {
	ids := []string{}
	for _, id := range IdByCountryCode[cc] {
		ids = append(ids, fmt.Sprint(id))
	}

	return ids
}
