package db

import (
	"launchbot/users"
	"math"
	"time"

	"github.com/rs/zerolog/log"
)

type NotificationTime struct {
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

	NETUnix     int64  // Calculated from NET
	WebcastLink string // Highest-priority link from VidURLs

	// Manually, stored in the database (capture on cache updates)
	Sent24h  bool
	Sent12h  bool
	Sent1h   bool
	Sent5min bool

	// Status of notification sends (e.g. "24hour": false), not embedded
	Notifications NotificationStates `gorm:"-:all"`

	// Information not dumped into the database (-> manual parse on a cache init from db)
	InfoURL []ContentURL `json:"infoURLs" gorm:"-:all"` // Not embedded into db: parsed manually
	VidURL  []ContentURL `json:"vidURLs" gorm:"-:all"`  // Not embedded into db: parsed manually
}

// Maps the send times to send states.
// Keys: (24hour, 12hour, 1hour, 5min)
// Value: bool, indicating sent status
type NotificationStates map[string]bool

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

// TODO implement in the cache?
var LSPShorthands = map[int]string{
	63:  "ROSCOSMOS",
	115: "Arianespace",
	124: "ULA",
	147: "Rocket Lab",
	265: "Firefly",
	285: "Astra",
}

// Extends the Launch struct to add a .PostponeNotify() method.
func (launch *Launch) PostponeNotify(postponedTo int) {
}

// Pulls recipients for this notification type from the DB
func (launch *Launch) GetRecipients(db *Database, notifType NotificationTime) *users.UserList {
	// TODO Implement
	recipients := users.UserList{Platform: "tg", Users: []*users.User{}}
	user := users.User{Platform: recipients.Platform, Id: db.Owner}

	recipients.Add(user, true)

	return &recipients
}

//  Returns the first unsent notification type for a launch
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
