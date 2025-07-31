package users

import (
	"fmt"
	"launchbot/stats"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type User struct {
	Id                    string `gorm:"primaryKey;index:enabled;index:disabled"`
	Platform              string `gorm:"primaryKey"` // "tg", "dg"
	Type                  ChatType
	Locale                string   // E.g. "Europe/Berlin"
	Time                  UserTime `gorm:"-:all"`
	Enabled24h            bool     `gorm:"index:enabled;index:disabled;default:1"`
	Enabled12h            bool     `gorm:"index:enabled;index:disabled;default:0"`
	Enabled1h             bool     `gorm:"index:enabled;index:disabled;default:0"`
	Enabled5min           bool     `gorm:"index:enabled;index:disabled;default:1"`
	EnabledPostpone       bool     `gorm:"index:enabled;index:disabled;default:1"`
	AnyoneCanSendCommands bool     // Group setting to enable non-admins to call commands
	SubscribedAll         bool     `gorm:"index:enabled;index:disabled"`
	SubscribedTo          string   // List of comma-separated LSP IDs
	UnsubscribedFrom      string   // List of comma-separated LSP IDs
	MutedLaunches         string   // A comma-separated string of muted launches by ID
	MutedKeywords         string   // Comma-separated keywords to exclude from notifications
	SubscribedKeywords    string   // Comma-separated keywords to include in notifications
	FilterMode            string   // "exclude" (default), "include", or "hybrid"
	SubscribedNewsletter  bool
	MigratedFromId        string     // If the chat has been migrated, keep its original id
	Stats                 stats.User `gorm:"embedded"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LastActive            time.Time        `gorm:"-:all"` // Track when chat was last active
	LastActivityType      LastActivityType `gorm:"-:all"`
}

type ChatType string
type LastActivityType string

const (
	Private      ChatType         = "private"
	Group        ChatType         = "group"
	Channel      ChatType         = "channel"
	Notification LastActivityType = "notification"
	Interaction  LastActivityType = "interaction"
)

// User-time, to help with caching and minimize DB reads
type UserTime struct {
	Location  *time.Location // User's time zone for the Time-module
	UtcOffset string         // A legible UTC offset string, e.g. "UTC+5"
}

type UserCache struct {
	Users   []*User
	InCache []string
	Mutex   sync.Mutex
}

// Extends the User type by creating a list of users.
// This can be userful for e.g. sending notifications to one platform.
type UserList struct {
	Platform string
	Users    []*User
	Mutex    sync.Mutex
}

// Saves a user into a database over Gorm
func (user *User) SaveIntoDatabase(tx *gorm.DB) error {
	// Load user
	temp := User{}
	result := tx.First(&temp, "Id = ? AND platform = ?", user.Id, user.Platform)

	// Set time zone when function returns
	defer user.SetTimeZone()

	switch result.Error {
	case nil:
		// No errors: user exists, save
		result = tx.Save(user)
	case gorm.ErrRecordNotFound:
		// Record doesn't exist: insert as new
		result = tx.Create(user)
	default:
		// Other error: log
		log.Error().Err(result.Error).Msgf("Error finding chat with id=%s:%s", user.Id, user.Platform)
		return result.Error
	}

	if result.Error != nil {
		log.Error().Err(result.Error).Msgf("Failed to save chat id=%s:%s", user.Id, user.Platform)
		return result.Error
	}

	return nil
}

// Toggle mute for launch with id
func (user *User) ToggleLaunchMute(id string, toggleTo bool) bool {
	// Verify the new state does not match the existing state
	if toggleTo == user.HasMutedLaunch(id) {
		log.Warn().Msgf("New mute status equals current mute status! Id=%s, user=%s, state=%v",
			id, user.Id, toggleTo)
		return true
	}

	// If launch is being muted, just append it to the field of muted launches
	if toggleTo == true {
		if user.MutedLaunches == "" {
			// If user has no muted launches, add this ID only
			user.MutedLaunches = id
		} else {
			// Split into slice by commas, append id, re-join into a string
			user.MutedLaunches = strings.Join(
				append(strings.Split(user.MutedLaunches, ","), id),
				",")
		}
		return true
	}

	// Launch is being unmuted: remove it from the list
	mutedLaunches := strings.Split(user.MutedLaunches, ",")

	for idx, mutedId := range mutedLaunches {
		if id == mutedId {
			mutedLaunches = append(mutedLaunches[:idx], mutedLaunches[idx+1:]...)
			break
		}
	}

	// Re-join slice into a comma-separated string
	if len(mutedLaunches) > 0 {
		user.MutedLaunches = strings.Join(mutedLaunches, ",")
	} else {
		user.MutedLaunches = ""
	}

	return true
}

// Check if user has muted launch
func (user *User) HasMutedLaunch(id string) bool {
	if user.MutedLaunches == "" {
		return false
	}

	for _, launchId := range strings.Split(user.MutedLaunches, ",") {
		if launchId == id {
			return true
		}
	}

	return false
}

// Add a keyword to muted keywords list
func (user *User) AddMutedKeyword(keyword string) bool {
	// Check if keyword already exists
	if user.HasMutedKeyword(keyword) {
		return false
	}

	if user.MutedKeywords == "" {
		user.MutedKeywords = keyword
	} else {
		user.MutedKeywords = strings.Join(append(strings.Split(user.MutedKeywords, ","), keyword), ",")
	}
	return true
}

// Remove a keyword from muted keywords list
func (user *User) RemoveMutedKeyword(keyword string) bool {
	if !user.HasMutedKeyword(keyword) {
		return false
	}

	keywords := strings.Split(user.MutedKeywords, ",")
	for idx, k := range keywords {
		if strings.EqualFold(k, keyword) {
			keywords = append(keywords[:idx], keywords[idx+1:]...)
			break
		}
	}

	if len(keywords) > 0 {
		user.MutedKeywords = strings.Join(keywords, ",")
	} else {
		user.MutedKeywords = ""
	}
	return true
}

// Check if user has muted a keyword
func (user *User) HasMutedKeyword(keyword string) bool {
	if user.MutedKeywords == "" {
		return false
	}

	for _, k := range strings.Split(user.MutedKeywords, ",") {
		if strings.EqualFold(k, keyword) {
			return true
		}
	}
	return false
}

// Add a keyword to subscribed keywords list
func (user *User) AddSubscribedKeyword(keyword string) bool {
	// Check if keyword already exists
	if user.HasSubscribedKeyword(keyword) {
		return false
	}

	if user.SubscribedKeywords == "" {
		user.SubscribedKeywords = keyword
	} else {
		user.SubscribedKeywords = strings.Join(append(strings.Split(user.SubscribedKeywords, ","), keyword), ",")
	}
	return true
}

// Remove a keyword from subscribed keywords list
func (user *User) RemoveSubscribedKeyword(keyword string) bool {
	if !user.HasSubscribedKeyword(keyword) {
		return false
	}

	keywords := strings.Split(user.SubscribedKeywords, ",")
	for idx, k := range keywords {
		if strings.EqualFold(k, keyword) {
			keywords = append(keywords[:idx], keywords[idx+1:]...)
			break
		}
	}

	if len(keywords) > 0 {
		user.SubscribedKeywords = strings.Join(keywords, ",")
	} else {
		user.SubscribedKeywords = ""
	}
	return true
}

// Check if user has subscribed to a keyword
func (user *User) HasSubscribedKeyword(keyword string) bool {
	if user.SubscribedKeywords == "" {
		return false
	}

	for _, k := range strings.Split(user.SubscribedKeywords, ",") {
		if strings.EqualFold(k, keyword) {
			return true
		}
	}
	return false
}

// Check if a launch matches user's keyword filters
func (user *User) MatchesKeywordFilter(launchName, vehicleName, missionName string) bool {
	// Get filter mode, defaulting to exclude if not set
	filterMode := user.FilterMode
	if filterMode == "" {
		filterMode = "exclude"
	}

	// Combine all text to search
	searchText := strings.ToLower(launchName + " " + vehicleName + " " + missionName)

	switch filterMode {
	case "exclude":
		// Check if any muted keyword matches
		if user.MutedKeywords != "" {
			for _, keyword := range strings.Split(user.MutedKeywords, ",") {
				if strings.Contains(searchText, strings.ToLower(keyword)) {
					return false // Exclude this launch
				}
			}
		}
		return true // Include by default in exclude mode

	case "include":
		// Only include if subscribed keyword matches
		if user.SubscribedKeywords == "" {
			return true // If no keywords set, include all
		}
		for _, keyword := range strings.Split(user.SubscribedKeywords, ",") {
			if strings.Contains(searchText, strings.ToLower(keyword)) {
				return true // Include this launch
			}
		}
		return false // Exclude by default in include mode

	case "hybrid":
		// First check subscribed keywords (include takes precedence)
		hasSubscribedMatch := false
		if user.SubscribedKeywords != "" {
			for _, keyword := range strings.Split(user.SubscribedKeywords, ",") {
				if strings.Contains(searchText, strings.ToLower(keyword)) {
					hasSubscribedMatch = true
					break
				}
			}
			// If we have subscribed keywords but no match, exclude
			if !hasSubscribedMatch {
				return false
			}
		}

		// Then check muted keywords
		if user.MutedKeywords != "" {
			for _, keyword := range strings.Split(user.MutedKeywords, ",") {
				if strings.Contains(searchText, strings.ToLower(keyword)) {
					return false // Exclude this launch
				}
			}
		}
		return true

	default:
		// Invalid mode, default to include
		return true
	}
}

// Return a bool indicating if user has any notification subscription times enabled
func (user *User) AnyNotificationTimesEnabled() bool {
	// Beautiful and concise at only 105 characters 8)
	return (user.Enabled24h || user.Enabled12h || user.Enabled1h || user.Enabled5min || user.EnabledPostpone)
}

// Returns a list of integers for all enabled and disabled launch provider IDs
func (user *User) GetNotificationStates() ([]int, []int) {
	var (
		enabledIds  []int
		disabledIds []int
		intId       int
	)

	if user.SubscribedTo != "" {
		for _, strId := range strings.Split(user.SubscribedTo, ",") {
			intId, _ = strconv.Atoi(strId)
			enabledIds = append(enabledIds, intId)
		}
	}

	if user.UnsubscribedFrom != "" {
		for _, strId := range strings.Split(user.UnsubscribedFrom, ",") {
			intId, _ = strconv.Atoi(strId)
			disabledIds = append(disabledIds, intId)
		}
	}

	return enabledIds, disabledIds
}

// Get an id:enabled_bool map for all launch provider IDs for this user
func (user *User) GetNotificationStateMap() map[int]bool {
	enabled, disabled := user.GetNotificationStates()
	stateMap := map[int]bool{}

	for _, id := range enabled {
		stateMap[id] = true
	}

	for _, id := range disabled {
		stateMap[id] = false
	}

	return stateMap
}

// Get user's subscription status by launch provider ID
func (user *User) GetNotificationStatusById(id int) bool {
	// If user has subscribed to this ID, return true
	if strings.Contains(user.SubscribedTo, fmt.Sprint(id)) {
		return true
	}

	// If user has the all-flag flipped, and has not unsubscribed from this ID, return true
	if user.SubscribedAll && !strings.Contains(user.UnsubscribedFrom, fmt.Sprint(id)) {
		return true
	}

	// Otherwise, return false
	return false
}

// Set the SubscribedAll flag for a user, and do some special handling
func (user *User) SetAllFlag(newState bool) {
	// Flip the flag
	user.SubscribedAll = newState

	// Default manual fields: nothing is unsubscribed from, and everything is subscribed to
	user.SubscribedTo = ""
	user.UnsubscribedFrom = ""
}

// Toggle a single notification-time subscription status
func (user *User) SetNotificationTimeFlag(flagName string, newState bool) {
	switch flagName {
	case "24h":
		user.Enabled24h = newState
	case "12h":
		user.Enabled12h = newState
	case "1h":
		user.Enabled1h = newState
	case "5min":
		user.Enabled5min = newState
	case "postpone":
		user.EnabledPostpone = newState
	default:
		log.Warn().Msgf("Invalid flag in SetNotificationTimeFlag: %s", flagName)
	}

	// Disable postpone notifications if user disables all other notification types
	// User can still explicitly enable only postpone notifications.
	if !user.Enabled24h && !user.Enabled12h && !user.Enabled1h && !user.Enabled5min {
		if flagName != "postpone" {
			user.EnabledPostpone = false
		}
	}
}

// Has user subscribed to all notification times?
func (user *User) AllNotificationTimesEnabled() bool {
	return user.Enabled24h && user.Enabled12h && user.Enabled1h && user.Enabled5min && user.EnabledPostpone
}

// Toggle subscription status for a list of launch provider IDs
func (user *User) ToggleIdSubscription(ids []string, newState bool) {
	// Load notification states as a map of id:bool for easy updates
	stateMap := user.GetNotificationStateMap()

	// Convert to an int, set in map
	for _, id := range ids {
		idInt, _ := strconv.Atoi(id)

		if newState == true && user.SubscribedAll || newState == false && !user.SubscribedAll {
			delete(stateMap, idInt)
		} else {
			stateMap[idInt] = newState
		}
	}

	// Save updated map
	user.SaveFromMap(stateMap)
}

// Toggle the status of a command permission flag
func (user *User) ToggleCommandPermissionStatus(permission string, newState bool) {
	switch permission {
	case "all":
		log.Debug().Msgf("Chat=%s toggled AnyoneCanSendCommands to %v", user.Id, newState)
		user.AnyoneCanSendCommands = newState
	default:
		log.Warn().Msgf("Got unknown data in ToggleCommandPermissionStatus(): %s", permission)
	}
}

// Save launch-provider notification subscription states from map to user
func (user *User) SaveFromMap(stateMap map[int]bool) {
	enabled := []string{}
	disabled := []string{}

	for id, state := range stateMap {
		if state == true {
			enabled = append(enabled, fmt.Sprint(id))
		} else {
			disabled = append(disabled, fmt.Sprint(id))
		}
	}

	// Join slices and assign
	if len(enabled) != 0 {
		user.SubscribedTo = strings.Join(enabled, ",")
	} else {
		user.SubscribedTo = ""
	}

	if len(disabled) != 0 {
		user.UnsubscribedFrom = strings.Join(disabled, ",")
	} else {
		user.UnsubscribedFrom = ""
	}
}

// Sets the user's user.Time field. Called when user is loaded from or saved to DB.
func (user *User) SetTimeZone() {
	// If locale is empty, the default is UTC
	tz, err := time.LoadLocation(user.Locale)

	if err != nil {
		log.Error().Err(err).Msgf("Error loading time zone for user with locale='%s': defaulting...", user.Locale)

		// Default to UTC
		tz, err = time.LoadLocation("")

		if err != nil {
			// This should never happen, but handle it anyway
			log.Error().Err(err).Msgf("Loading default location for user failed in SetTimeZone()")
		}

		// Set defaults
		user.Time = UserTime{
			Location:  tz,
			UtcOffset: "UTC",
		}

		return
	}

	// Create time field for user, initialize with "UTC"
	user.Time = UserTime{
		Location:  tz,
		UtcOffset: "UTC",
	}

	// Get offset from user's current time
	_, offset := time.Now().In(tz).Zone()

	// If offset is zero, we can leave it as "UTC"
	if offset == 0 {
		return
	}

	// Append a plus to UtcOffset string if the offset is positive
	user.Time.UtcOffset += map[bool]string{true: "+", false: ""}[offset >= 0]

	if offset%3600 == 0 {
		// If divisible by 3600, the offset is an integer hour
		user.Time.UtcOffset += fmt.Sprintf("%d", offset/3600)
	} else {
		// Extract whole hours from the second offset
		hours := (offset - (offset % 3600)) / 3600
		mins := (offset % 3600) / 60

		user.Time.UtcOffset += fmt.Sprintf("%d:%2d", hours, mins)
	}
}

// Deletes the time zone from cache
func (user *User) DeleteTimeZone() {
	user.Locale = ""
	user.SetTimeZone()
}

// Returns either the current time zone locale, or a string to indicate the lack of stored info
func (user *User) SavedTimeZoneInfo() string {
	if user.Locale == "" {
		return "None (UTC+0)"
	}

	return fmt.Sprintf("%s (%s)", user.Locale, user.Time.UtcOffset)
}

// Load user's notification time preferences into a map
func (user *User) NotificationTimePreferenceMap() map[string]bool {
	return map[string]bool{
		"24h":  user.Enabled24h,
		"12h":  user.Enabled12h,
		"1h":   user.Enabled1h,
		"5min": user.Enabled5min,
	}
}
