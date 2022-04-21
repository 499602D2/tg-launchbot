package db

import (
	"fmt"
	"launchbot/users"
	"launchbot/utils"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dustin/go-humanize"
	emoji "github.com/jayco/go-emoji-flag"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// TODO save in a database table + cache
// V3.1 (load IDs from LL2)
var LSPShorthands = map[int]string{
	63:  "ROSCOSMOS",
	96:  "KhSC",
	115: "Arianespace",
	124: "ULA",
	147: "Rocket Lab",
	265: "Firefly",
	285: "Astra",
}

type Notification struct {
	Type       string // In (24hour, 12hour, 1hour, 5min)
	SendTime   int64  // Unix-time of the notification
	AllSent    bool   // All notifications sent already?
	LaunchId   string
	LaunchName string
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
	Url            string         `json:"url"`

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
func (launch *Launch) NotificationMessage() (string, *Notification) {
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

	return text, &notification
}

// Creates a schedule message from the launch cache
// TODO simplify, now that launch cache is truly ordered (do in one loop)
func (cache *Cache) ScheduleMessage(user *users.User, showMissions bool) string {
	if user.Time == (users.UserTime{}) {
		user.LoadTimeZone()
	}

	// List of launch-lists, one per launch date
	schedule := [][]*Launch{}

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
			schedule = append(schedule, []*Launch{launch})

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

// Extends the Launch struct to add a .PostponeNotify() method.
func (launch *Launch) PostponeNotify(postponedTo int) {
}

// Pulls recipients for this notification type from the DB
func (launch *Launch) GetRecipients(db *Database, notifType *Notification) *users.UserList {
	// TODO Implement
	recipients := users.UserList{Platform: "tg", Users: []*users.User{}}
	user := users.User{Platform: recipients.Platform, Id: fmt.Sprint(db.Owner)}

	recipients.Add(user, true)

	return &recipients
}

//  Returns the first unsent notification type for a launch
func (launch *Launch) NextNotification() Notification {
	// TODO do this smarter instead of re-declaring a billion times
	NotificationSendTimes := map[string]time.Duration{
		"Sent24h":  time.Duration(24) * time.Hour,
		"Sent12h":  time.Duration(12) * time.Hour,
		"Sent1h":   time.Duration(1) * time.Hour,
		"Sent5min": time.Duration(5) * time.Minute,
	}

	// Minutes the send-time is allowed to slip by
	allowedSlipMins := 5

	for notifType, sent := range launch.NotificationState.Map {
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

					notifType = strings.ReplaceAll(notifType, "Sent", "")
					return Notification{Type: notifType, SendTime: sendTime,
						LaunchId: launch.Id, LaunchName: launch.Name, Count: 1}
				}
			}

			// Sent is false and has not been missed: return type
			notifType = strings.ReplaceAll(notifType, "Sent", "")
			return Notification{Type: notifType, SendTime: sendTime,
				LaunchId: launch.Id, LaunchName: launch.Name, Count: 1}
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
		return LSPShorthands[provider.Id]
	}

	// Log long names we encounter
	if len(provider.Name) > len("Rocket Lab") {
		log.Warn().Msgf("Provider name '%s' not found in LSPShorthands, id=%d",
			provider.Name, provider.Id)
		return provider.Abbrev
	}

	return provider.Name
}

// Updates the notification-state map from the boolean values
func (state *NotificationState) UpdateMap() {
	state.Map = map[string]bool{
		"Sent24h":  state.Sent24h,
		"Sent12h":  state.Sent12h,
		"Sent1h":   state.Sent1h,
		"Sent5min": state.Sent5min,
	}
}

// Updates the notification-state booleans from the map
func (state *NotificationState) UpdateFlags() {
	state.Sent24h = state.Map["Sent24h"]
	state.Sent12h = state.Map["Sent12h"]
	state.Sent1h = state.Map["Sent1h"]
	state.Sent5min = state.Map["Sent5min"]
}

// A simple wrapper to initialize a new notification state from struct's boolean values
func (state *NotificationState) Load() {
	state.UpdateMap()
}
