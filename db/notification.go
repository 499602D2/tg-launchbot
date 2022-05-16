package db

import (
	"fmt"
	"launchbot/users"
	"strings"
	"time"

	"github.com/hako/durafmt"
	"github.com/rs/zerolog/log"
)

type Postpone struct {
	PostponedBy int64           // Seconds the launch was postponed by
	ResetStates map[string]bool // Keep each notification state that was reset (e.g. "24h": true)
}

type Notification struct {
	Type       string   // In (24h, 12h, 1h, 5min, postpone)
	SendTime   int64    // Unix-time of the notification
	AllSent    bool     // All notifications sent already?
	LaunchId   string   // Launch ID associated
	LaunchName string   // Name of launch
	Count      int      // If more than one, list their count
	IDs        []string // If more than one, include their IDs here
}

// Updates the notification-state map from the boolean values
func (state *NotificationState) UpdateMap() NotificationState {
	state.Map = map[string]bool{
		"Sent24h":  state.Sent24h,
		"Sent12h":  state.Sent12h,
		"Sent1h":   state.Sent1h,
		"Sent5min": state.Sent5min,
	}

	return *state
}

// Updates the notification-state booleans from the map
func (state *NotificationState) UpdateFlags() NotificationState {
	// Update booleans
	state.Sent24h = state.Map["Sent24h"]
	state.Sent12h = state.Map["Sent12h"]
	state.Sent1h = state.Map["Sent1h"]
	state.Sent5min = state.Map["Sent5min"]
	return *state
}

// Return a boolean, indicating whether any notifications have been sent for this launch
func (state *NotificationState) AnyNotificationsSent() bool {
	for _, state := range state.Map {
		if state {
			return true
		}
	}

	return false
}

// Functions checks if a NET slip of $slip seconds resets any notification send states
func (launch *Launch) AnyStatesResetByNetSlip(slip int64) (bool, map[string]bool) {
	// Map notification types to seconds
	notificationTypePreSendTime := map[string]int64{
		"24h": 24 * 3600, "12h": 12 * 3600, "1h": 3600, "5min": 5 * 60,
	}

	// Track states that are reset, if any
	resetStates := map[string]bool{
		"24h": false, "12h": false, "1h": false, "5min": false,
	}

	// Only do a disk op if a state was altered
	stateAltered := false

	// Loop over current notification send states
	for notification, sent := range launch.NotificationState.Map {
		if !sent {
			continue
		}

		// Get time this notification window ends at
		windowEndTime := launch.NETUnix - notificationTypePreSendTime[notification]

		// Time until window end, plus NET slip
		windowDelta := windowEndTime - time.Now().Unix() + slip

		// Check if the NET slip puts us back before this notification window
		if windowEndTime > time.Now().Unix()-slip {
			// Launch was postponed: flip the notification state
			launch.NotificationState.Map[notification] = false

			// Record the state being reset
			resetStates[notification] = true
			stateAltered = true

			log.Debug().Msgf("Launch had its notification state reset with delta of %s (type=%s, launch=%s)",
				durafmt.ParseShort(time.Duration(windowDelta)*time.Second), notification, launch.Id)
		}
	}

	// Save updated states
	if stateAltered {
		launch.NotificationState.UpdateFlags()
		return true, resetStates
	}

	return false, resetStates
}

// Gets all notification recipients for this notification
func (launch *Launch) NotificationRecipients(db *Database, notificationType string, platform string) []*users.User {
	usersWithNotificationEnabled := []*users.User{}

	// Map notification type to a database table name
	tableNotifType := ""

	if notificationType == "postpone" {
		tableNotifType = "enabled_postpone"
	} else {
		tableNotifType = fmt.Sprintf("enabled%s", notificationType)
	}

	// Get all chats that have this notification type enabled
	result := db.Conn.Model(&users.User{}).Where(
		fmt.Sprintf("%s = ? AND platform = ?", tableNotifType), 1, platform,
	).Find(&usersWithNotificationEnabled)

	if result.Error != nil {
		log.Error().Err(result.Error).Msg("Error loading notification recipient list")
	}

	// List of final recipients
	recipients := []*users.User{}

	// Filter all users from the list
	for _, user := range usersWithNotificationEnabled {
		// Check if user is subscribed to this provider
		if !user.GetNotificationStatusById(launch.LaunchProvider.Id) {
			log.Debug().Msgf("User=%s is not subscribed to provider with id=%d", user.Id, launch.LaunchProvider.Id)
			continue
		}

		// Check if user has this specific launch muted
		if user.HasMutedLaunch(launch.Id) {
			log.Debug().Msgf("User=%s has muted launch with id=%s", user.Id, launch.Id)
			continue
		}

		// User has subscribed to this launch, and has not muted it: add to recipients
		log.Debug().Msgf("Adding user=%s to recipients", user.Id)
		recipients = append(recipients, user)
	}

	return recipients
}

// Load IDs into a map
func (launch *Launch) LoadSentNotificationIdMap() map[string]string {
	sentIds := map[string]string{}

	// A comma-separated slice of chat_id:msg_id strings
	for _, idPair := range strings.Split(launch.SentNotificationIds, ",") {
		// User id (0), message id (1)
		ids := strings.Split(idPair, ":")
		sentIds[ids[0]] = ids[1]
	}

	return sentIds
}

// Saves the sent notification message IDs for a launch
func (launch *Launch) SaveSentNotificationIds(ids []string, db *Database) {
	// Join the IDs
	launch.SentNotificationIds = strings.Join(ids, ",")

	// Insert the launch back into the database
	err := db.Update([]*Launch{launch}, false, false)

	if err != nil {
		log.Error().Err(err).Msgf("Saving notification message IDs to disk failed")
	} else {
		log.Debug().Msgf("Sent notification message IDs saved")
	}
}