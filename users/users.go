package users

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type User struct {
	Platform string // Discord=dg, Telegram=tb, Email=email
	Id       int64
	TimeZone *time.Location
}

/*
Extends the User type by creating a list of users.
This can be userful for e.g. sending notifications to one platform.
*/
type UserList struct {
	Platform string
	Users    []*User
	Mutex    sync.Mutex
}

/* Loads the user's time zone information from cache/disk */
func (user *User) LoadTimeZone() {
	tz, err := time.LoadLocation("UTC")
	if err != nil {
		log.Error().Err(err).Msg("Error loading time zone for user")
		return
	}

	// Set the loaded time zone
	user.TimeZone = tz
	log.Warn().Msg("Proper time zone loading not implemented!")
}

/* Adds a single user to a UserList and adds a time zone if required */
func (userList *UserList) Add(user User, addTimeZone bool) {
	userList.Mutex.Lock()

	if addTimeZone {
		user.LoadTimeZone()
	}

	// Add user to the list
	userList.Users = append(userList.Users, &user)

	userList.Mutex.Unlock()
}
