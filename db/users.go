package db

import (
	"fmt"
	"launchbot/users"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Clean any users from the cache that have not been active in a while
func (cache *Cache) CleanUserCache(db *Database, force bool) {
	// Loop over users and clean all that shoud be expired
	usersToBeCleaned := []*users.User{}

	for _, user := range cache.Users.Users {
		if time.Since(user.LastActive) > time.Duration(1)*time.Hour || force {
			usersToBeCleaned = append(usersToBeCleaned, user)
		}
	}

	// Clean all users, and save stats before doing it
	for _, user := range usersToBeCleaned {
		db.SaveUser(user)
		cache.FlushUser(user.Id, user.Platform)
	}

	if len(usersToBeCleaned) > 0 {
		log.Debug().Msgf("Flushed %d user(s) from the cache, %d still cached",
			len(usersToBeCleaned), len(cache.Users.Users))
	}
}

// Searches for user from the cache, returning the existing user-pointer if found.
// If the user is not cached, the user given as input is inserted into the cache and returned.
// Returns true if user was found in the cache, false if user was not already cached.
func (cache *Cache) UseCachedUserIfExists(user *users.User, lockMutex bool) (*users.User, bool) {
	if lockMutex {
		cache.Users.Mutex.Lock()
		defer cache.Users.Mutex.Unlock()
	}

	// Search for the user in the cache
	cachedUser, insertAt := cache.UserIsCached(user)

	if cachedUser == nil {
		// User not cached, insert (mutex locked)
		cache.InsertIntoCache(user, insertAt, false)
		return user, false
	}

	// User is cached, return the found pointer
	return cachedUser, true
}

// Inserts a user into the cache
func (cache *Cache) InsertIntoCache(user *users.User, atIndex int, lockMutex bool) {
	// Set userCache ptr
	userCache := cache.Users

	if lockMutex {
		userCache.Mutex.Lock()
		defer userCache.Mutex.Unlock()
	}

	// Checks if the chat ID already exists
	if atIndex == -1 {
		atIndex = sort.SearchStrings(userCache.InCache, user.Id)
	}

	// Add user to cache so that the cache stays ordered
	if len(userCache.InCache) == atIndex {
		// Nil or empty slice, or after last element
		userCache.Users = append(userCache.Users, user)
		userCache.InCache = append(userCache.InCache, user.Id)
	} else if atIndex == 0 {
		// If zeroth index, append
		userCache.Users = append([]*users.User{user}, userCache.Users...)
		userCache.InCache = append([]string{user.Id}, userCache.InCache...)
	} else {
		// Otherwise, we're inserting in the middle of the array
		userCache.Users = append(userCache.Users[:atIndex+1], userCache.Users[atIndex:]...)
		userCache.Users[atIndex] = user

		userCache.InCache = append(userCache.InCache[:atIndex+1], userCache.InCache[atIndex:]...)
		userCache.InCache[atIndex] = user.Id
	}
}

// Return user if the user is cached, otherwise a nil and the idx the user should be inserted at
func (cache *Cache) UserIsCached(user *users.User) (*users.User, int) {
	// Set userCache ptr
	userCache := cache.Users

	i := sort.SearchStrings(userCache.InCache, user.Id)

	if len(userCache.InCache) > 0 {
		if i < len(userCache.InCache) && userCache.Users[i].Id == user.Id {
			// User is in cache, return
			return userCache.Users[i], i
		}
	}

	// User not found
	return nil, i
}

// Finds a user from the user-cache and returns the user and the index the user was found at
func (cache *Cache) FindUser(id string, platform string) *users.User {
	// Set userCache ptr
	userCache := cache.Users

	// Lock mutex
	userCache.Mutex.Lock()
	defer userCache.Mutex.Unlock()

	// Checks if the chat ID already exists
	i := sort.SearchStrings(userCache.InCache, id)

	if len(userCache.InCache) > 0 {
		if i < len(userCache.InCache) && userCache.Users[i].Id == id {
			// User is in cache, return
			return userCache.Users[i]
		}
	}

	// User is not in cache; load from db (also sets time zone)
	user := cache.Database.LoadUser(id, platform)

	// Add user to cache
	cache.InsertIntoCache(user, i, false)

	return user
}

// Flushes a single user from the user cache
func (cache *Cache) FlushUser(id string, platform string) {
	// Lock mutex while doing cache ops
	cache.Users.Mutex.Lock()
	defer cache.Users.Mutex.Unlock()

	// Pointer to the user-cache
	userCache := cache.Users

	// Checks if the chat ID already exists
	i := sort.SearchStrings(userCache.InCache, id)

	if len(userCache.InCache) > 0 {
		if i < len(userCache.InCache) && userCache.Users[i].Id == id {
			// User is in cache: flush from list of user pointers
			userCache.Users = append(userCache.Users[:i], userCache.Users[i+1:]...)

			// Flush from slice of user IDs in cache
			userCache.InCache = append(userCache.InCache[:i], userCache.InCache[i+1:]...)
		}
	}

	// log.Debug().Msgf("Flushed user=%s", id)
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
		return &user
	case gorm.ErrRecordNotFound:
		// Record doesn't exist: insert as new
		log.Debug().Msgf("Chat not found in db: inserting as new with id=%s", user.Id)

		// Keep track of when user was subscribed (mainly for v2 -> v3 migration)
		user.Stats.SubscribedSince = time.Now().Unix()

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

// Save a batch of users to disk
func (db *Database) SaveUserBatch(users []*users.User) {
	// Do a single batch upsert (Update all columns, except primary keys, to new value on conflict)
	// https://gorm.io/docs/create.html#Upsert-x2F-On-Conflict
	result := db.Conn.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(&users)

	if result.Error != nil {
		log.Error().Err(result.Error).Msg("Batch insert of users failed in SaveUserBatch")
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
