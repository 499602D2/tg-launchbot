package users

import (
	"fmt"
	"launchbot/stats"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type User struct {
	Id                   string   `gorm:"primaryKey;index:enabled;index:disabled"`
	Platform             string   `gorm:"primaryKey"`
	Locale               string   // E.g. "Europe/Berlin"
	Time                 UserTime `gorm:"-:all"`
	Enabled24h           bool     `gorm:"index:enabled;index:disabled"`
	Enabled12h           bool     `gorm:"index:enabled;index:disabled"`
	Enabled1h            bool     `gorm:"index:enabled;index:disabled"`
	Enabled5min          bool     `gorm:"index:enabled;index:disabled"`
	SubscribedAll        bool     `gorm:"index:enabled;index:disabled"`
	SubscribedNewsletter bool
	SubscribedTo         string     // List of LSP IDs
	UnsubscribedFrom     string     // List of LSP IDs
	Stats                stats.User `gorm:"embedded"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DeletedAt            gorm.DeletedAt `gorm:"index"`
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
}

// Extends the User type by creating a list of users.
// This can be userful for e.g. sending notifications to one platform.
type UserList struct {
	Platform string
	Users    []*User
	Mutex    sync.Mutex
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
