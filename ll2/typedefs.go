package ll2

/*
Implements an incomplete LL2 API type wrapper, meant for
consuming mainly the /launch endpoint.

Documentation: https://ll.thespacedevs.com/2.2.0/swagger
*/

/*
TODO replace with something more sustainable
- Maps a long LSP name to a short, friendly name

Alternatively:
- map ID to a short name
	- cheap callbacks (/notify/enable/73)
	- IDs don't change
	- if ID not in list of shorthands, use abbreviation
*/
var LSPShorthands = map[string]string{
	"China Aerospace Science and Technology Corporation": "CASC",
}

type LaunchUpdate struct {
	Count    int
	Launches []*Launch `json:"results"`
}

type Launch struct {
	// Information we get from the API
	Id             string         `json:"id"`
	Url            string         `json:"url"`
	Slug           string         `json:"slug"`
	Name           string         `json:"name"`
	Status         LaunchStatus   `json:"status"`
	LastUpdated    string         `json:"last_updated"`
	NET            string         `json:"net"`
	WindowEnd      string         `json:"window_end"`
	WindowStart    string         `json:"window_start"`
	Probability    int            `json:"probability"`
	HoldReason     string         `json:"holdreason"`
	FailReason     string         `json:"failreason"`
	LaunchProvider LaunchProvider `json:"launch_service_provider"`
	Rocket         Rocket         `json:"rocket"`
	Mission        Mission        `json:"mission"`
	LaunchPad      LaunchPad      `json:"pad"`
	InfoURL        []ContentURL   `json:"infoURLs"`
	VidURL         []ContentURL   `json:"vidURLs"`
	WebcastIsLive  bool           `json:"webcast_live"`

	// Manually parsed information
	NETUnix       int64
	Postponed     bool               // Toggled if the launch was postponed in the update
	PostponedBy   int64              // Seconds the launch was postponed by
	Notifications NotificationStates // Status of notification sends (e.g. "24hour": false)
}

/*
Maps the send times to send states.

Keys: (24hour, 12hour, 1hour, 5min)
Value: bool, indicating sent status
*/
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
	Id   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`

	// TODO Rarely given: manually parse from the URL endpoint given -> save
	CountryCode string `json:"country_code"`
}

type Rocket struct {
	Id     int                 `json:"id"`
	Config RocketConfiguration `json:"configuration"`

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
	Orbit       Orbit  `json:"orbit"`
}

type Orbit struct {
	Id     int    `json:"id"`
	Name   string `json:"name"`   // e.g. "Low Earth Orbit"
	Abbrev string `json:"abbrev"` // e.g. "LEO"
}

type LaunchPad struct {
	Name             string      `json:"name"`
	Location         PadLocation `json:"location"`
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
