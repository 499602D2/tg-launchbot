package db

import (
	"launchbot/users"
	"sort"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// Finds a user from the user-cache and returns the user
func (cache *Cache) FindUser(id string, platform string) *users.User {
	// Set userCache ptr
	userCache := cache.Users

	// Checks if the chat ID already exists
	i := sort.SearchStrings(userCache.InCache, id)

	if i < userCache.Count && userCache.Users[i].Id == id {
		// User is in cache
		//log.Debug().Msgf("Loaded user=%s:%s from cache", userCache.Users[i].Id, userCache.Users[i].Platform)
		return userCache.Users[i]
	}

	// User is not in cache; load from db (also sets time zone)
	user := cache.Database.LoadUser(id, platform)

	// Add user to cache so that the cache stays ordered
	if userCache.Count == i {
		// Nil or empty slice, or after last element
		userCache.Users = append(userCache.Users, user)
		userCache.InCache = append(userCache.InCache, user.Id)
	} else if i == 0 {
		// If zeroth index, append
		userCache.Users = append([]*users.User{user}, userCache.Users...)
		userCache.InCache = append([]string{user.Id}, userCache.InCache...)
	} else {
		// Otherwise, we're inserting in the middle of the array
		userCache.Users = append(userCache.Users[:i+1], userCache.Users[i:]...)
		userCache.Users[i] = user

		userCache.InCache = append(userCache.InCache[:i+1], userCache.InCache[i:]...)
		userCache.InCache[i] = user.Id
	}

	log.Debug().Msgf("Added user=%s:%s to cache", userCache.Users[i].Id, userCache.Users[i].Platform)
	userCache.Count++
	return user
}

// Load a user from the database. If the user is not found, initialize it in
// the database, and return the new entry.
func (db *Database) LoadUser(id string, platform string) *users.User {
	// Temporary user-struct
	user := users.User{Id: id, Platform: platform}

	// Check if user exists
	result := db.Conn.First(&user, "Id = ? AND platform = ?", id, platform)

	// Set time zone when function returns
	defer user.SetTimeZone()

	switch result.Error {
	case nil:
		// No errors: return loaded user
		log.Info().Msgf("Loaded chat=%s:%s from db", id, platform)
		return &user
	case gorm.ErrRecordNotFound:
		// Record doesn't exist: insert as new
		log.Info().Msgf("Chat not found: inserting with id=%s", user.Id)
		result = db.Conn.Create(&user)
	default:
		// Other error: log
		log.Error().Err(result.Error).Msgf("Error finding chat with id=%s:%s", id, platform)
		return &user
	}

	if result.Error != nil {
		log.Error().Err(result.Error).Msgf("Failed to insert chat id=%s:%s", id, platform)
	}

	return &user
}

// Save user to disk
func (db *Database) SaveUser(user *users.User) {
	// Load user
	temp := users.User{}
	result := db.Conn.First(&temp, "Id = ? AND platform = ?", user.Id, user.Platform)

	// Set time zone when function returns
	defer user.SetTimeZone()

	switch result.Error {
	case nil:
		// No errors: user exists, save
		result = db.Conn.Save(user)
	case gorm.ErrRecordNotFound:
		// Record doesn't exist: insert as new
		result = db.Conn.Create(user)
	default:
		// Other error: log
		log.Error().Err(result.Error).Msgf("Error finding chat with id=%s:%s", user.Id, user.Platform)
		return
	}

	if result.Error != nil {
		log.Error().Err(result.Error).Msgf("Failed to save chat id=%s:%s", user.Id, user.Platform)
	}
}

func (db *Database) RemoveUser(user *users.User) {
	result := db.Conn.Delete(user)
	if result.Error != nil {
		log.Error().Err(result.Error).Msgf("Error deleting user=%s:%s",
			user.Id, user.Platform)
	}

	if result.RowsAffected == 0 {
		log.Warn().Msgf("Tried to delete user=%s:%s, but no rows were affected",
			user.Id, user.Platform)
	} else {
		log.Info().Msgf("Deleted user=%s:%s, RowsAffected=%d",
			user.Id, user.Platform, result.RowsAffected)
	}
}
