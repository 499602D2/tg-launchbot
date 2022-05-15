package db

import (
	"fmt"
	"launchbot/users"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// Clean any users from the cache that have not been active in a while
func (cache *Cache) CleanUserCache(db *Database, force bool) {
	// Loop over users and clean all that shoud be expired
	usersToBeCleaned := []*users.User{}

	for _, user := range cache.Users.Users {
		if time.Since(user.LastActive) > time.Duration(30)*time.Minute || force {
			usersToBeCleaned = append(usersToBeCleaned, user)
		}
	}

	// Clean all users, and save stats before doing it
	for _, user := range usersToBeCleaned {
		db.SaveUser(user)
		cache.FlushUser(user.Id, user.Platform)
	}

	if len(usersToBeCleaned) > 0 {
		log.Debug().Msgf("Flushed %d user(s) from the cache", len(usersToBeCleaned))
	} else {
		log.Debug().Msgf("No users were flushed from the cache (%d cached user(s))", len(cache.Users.InCache))
	}
}

// Finds a user from the user-cache and returns the user
func (cache *Cache) FindUser(id string, platform string) *users.User {
	// Set userCache ptr
	userCache := cache.Users

	// Checks if the chat ID already exists
	i := sort.SearchStrings(userCache.InCache, id)

	if len(userCache.InCache) > 0 {
		if i < userCache.Count && userCache.Users[i].Id == id {
			// User is in cache, return
			return userCache.Users[i]
		}
	}

	// User is not in cache; load from db (also sets time zone)
	user := cache.Database.LoadUser(id, platform)

	// Lock mutex for insert
	userCache.Mutex.Lock()
	defer userCache.Mutex.Unlock()

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

	// log.Debug().Msgf("Added chat=%s:%s to cache", userCache.Users[i].Id, userCache.Users[i].Platform)
	userCache.Count++
	return user
}

// Flushes a single user from the user cache
func (cache *Cache) FlushUser(id string, platform string) {
	// Lock mutex while doing cache ops
	cache.Users.Mutex.Lock()
	defer cache.Users.Mutex.Unlock()

	// Extract current cache
	userCache := cache.Users

	// Checks if the chat ID already exists
	i := sort.SearchStrings(userCache.InCache, id)

	if i < userCache.Count && userCache.Users[i].Id == id {
		// User is in cache: flush from list of user pointers
		userCache.Users = append(userCache.Users[:i], userCache.Users[i+1:]...)

		// Flush from slice of user IDs in cache
		userCache.InCache = append(userCache.InCache[:i], userCache.InCache[i+1:]...)
	}
}

// Load a user from the database. If the user is not found, initialize it in
// the database, and return the new entry.
func (db *Database) LoadUser(id string, platform string) *users.User {
	// Temporary user-struct
	user := users.User{Id: id, Platform: platform, LastActive: time.Now()}

	// Check if user exists
	result := db.Conn.First(&user, "Id = ? AND platform = ?", id, platform)

	// Set time zone when function returns
	defer user.SetTimeZone()

	switch result.Error {
	case nil:
		// No errors: return loaded user
		// log.Info().Msgf("Loaded chat=%s:%s from db", id, platform)
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

// Remove a user from the database
func (db *Database) RemoveUser(user *users.User) {
	// Do an unscoped delete so we aren't left with ghost entries
	result := db.Conn.Unscoped().Delete(user)
	if result.Error != nil {
		log.Error().Err(result.Error).Msgf("Error deleting user=%s:%s",
			user.Id, user.Platform)
	}

	if result.RowsAffected == 0 {
		log.Warn().Msgf("Tried to delete user=%s:%s, but no rows were affected",
			user.Id, user.Platform)
	} else {
		log.Info().Msgf("Deleted user=%s:%s", user.Id, user.Platform)
	}

	// Flush user from the cache so it doesn't linger around
	db.Cache.FlushUser(user.Id, user.Platform)
}

// Migrate a chat to its new id
func (db *Database) MigrateGroup(fromId int64, toId int64, platform string) {
	// Find existing chat row
	chat := users.User{}
	result := db.Conn.First(&chat, "Id = ? AND platform = ?", fromId, platform)

	switch result.Error {
	case gorm.ErrRecordNotFound:
		// Nothing found?
		log.Debug().Msgf("Chat with fromId=%d not found during migration to id=%d?", fromId, toId)
		chat.Id = fmt.Sprint(toId)
		chat.Platform = platform
		chat.MigratedFromId = fmt.Sprint(fromId)
		db.SaveUser(&chat)
		return
	case nil:
		break
	default:
		// Default error handler
		log.Error().Err(result.Error).Msg("Unexpected error encountered during migration")
	}

	// Delete the chat from the database
	db.RemoveUser(&chat)

	// Set new ID and migratedFrom
	chat.Id = fmt.Sprint(toId)
	chat.MigratedFromId = fmt.Sprint(fromId)

	// Save new chat
	db.SaveUser(&chat)

	log.Info().Msgf("Migrated chat from id=%s to id=%s", chat.MigratedFromId, chat.Id)
}

// Load how many users have subscribed to any notifications
func (db *Database) GetSubscriberCount() int {
	// Select all chats with any notifications enabled, and at least one notification time enabled
	result := db.Conn.Where(
		"(subscribed_all = ? OR subscribed_to != ?) AND "+
			"(enabled24h != ? OR enabled12h != ? OR enabled1h != ? OR enabled5min != ?)",
		1, "", 0, 0, 0, 0).Find(&[]users.User{})

	return int(result.RowsAffected)
}
