package users

import (
	"fmt"
	"launchbot/stats"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type User struct {
	Id                    string   `gorm:"primaryKey;index:enabled;index:disabled"`
	Platform              string   `gorm:"primaryKey"`
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
	SubscribedNewsletter  bool
	MigratedFromId        string     // If the chat has been migrated, keep its original id
	Stats                 stats.User `gorm:"embedded"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// User-time, to help with caching and minimize DB reads
type UserTime struct {
	Location  *time.Location // User's time zone for the Time module
	UtcOffset string         // A legible UTC offset string, e.g. "UTC+5"
}

type UserCache struct {
	Users   []*User
	InCache []string
	Count   int
	Mutex   sync.Mutex
}

// Extends the User type by creating a list of users.
// This can be userful for e.g. sending notifications to one platform.
type UserList struct {
	Platform string
	Users    []*User
	Mutex    sync.Mutex
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

func (user *User) SetAllFlag(newState bool) {
	// Flip the flag
	user.SubscribedAll = newState

	// Remove any manually set IDs
	user.SubscribedTo = ""
	user.UnsubscribedFrom = ""
}

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
	}

	// Disable postpone notifications if user disables all other notification types
	if !user.Enabled24h && !user.Enabled12h && !user.Enabled1h && !user.Enabled5min {
		if flagName != "postpone" {
			user.EnabledPostpone = false
		}
	}
}

func (user *User) AllNotificationTimesEnabled() bool {
	return user.Enabled24h && user.Enabled12h && user.Enabled1h && user.Enabled5min && user.EnabledPostpone
}

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
		log.Error().Err(err).Msg("Error loading time zone for user")
		return
	}

	// Create time field for user
	user.Time = UserTime{
		Location:  tz,
		UtcOffset: "UTC",
	}

	// Get offset from user's current time
	userTime := time.Now().In(tz)
	_, offset := userTime.Zone()

	// Add a plus if the offset is positive
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

// Adds a single user to a UserList and adds a time zone if required
func (userList *UserList) Add(user *User, addTimeZone bool) {
	userList.Mutex.Lock()

	if addTimeZone {
		if user.Time == (UserTime{}) {
			user.SetTimeZone()
		}
	}

	// Add user to the list
	userList.Users = append(userList.Users, user)
	userList.Mutex.Unlock()
}

// Reduce boilterplate by creating a user-list with a single user
func SingleUserList(user *User, addTimeZone bool, platform string) *UserList {
	// Create list
	list := UserList{Platform: platform}

	// Add user, return
	list.Add(user, addTimeZone)
	return &list
}
