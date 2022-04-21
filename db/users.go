package db

import (
	"launchbot/users"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

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
		log.Error().Err(result.Error).Msgf("Failed to insert chat id=%s:%s",
			id, platform)
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
