package db

import (
	"github.com/rs/zerolog/log"
)

func (db *Database) createStatsTable() {
	// Create stats table
	_, err := db.Connection.Exec(`CREATE TABLE stats 
		(notifications INT, api_requests INT, commands INT,
		data INT, last_api_update INT, PRIMARY KEY (notifications, api_requests))`)

	if err != nil {
		log.Fatal().Err(err).Msg("Error creating statistics table")
	}

	// Initialize stats table
	_, err = db.Connection.Exec(`INSERT INTO stats 
	(notifications, api_requests, commands, data, last_api_update)
	VALUES (0, 0, 0, 0, 0)`)

	if err != nil {
		log.Fatal().Err(err).Msg("Error inserting initial values into stats table")
	}
}

func (db *Database) createChatsTable() {
	// Create chats table
	_, err := db.Connection.Exec(`CREATE TABLE chats
		(chat TEXT, subscribed_since INT, member_count INT,
		time_zone TEXT, time_zone_str TEXT, command_permissions TEXT, postpone_notify BOOLEAN,
		notify_time_pref TEXT, enabled_notifications TEXT, disabled_notifications TEXT,
		PRIMARY KEY (chat))`)

	if err != nil {
		log.Fatal().Err(err).Msg("Error creating chats table")
	}

	// Create indices
	_, err = db.Connection.Exec("CREATE INDEX chatenabled ON chats (chat, enabled_notifications)")
	if err != nil {
		log.Fatal().Err(err).Msg("Error creating chatenabled index")
	}

	_, err = db.Connection.Exec("CREATE INDEX chatdisabled ON chats (chat, disabled_notifications)")
	if err != nil {
		log.Fatal().Err(err).Msg("Error creating chatdisabled index")
	}
}

func (db *Database) createLaunchTable() {
	// Create launches table
	_, err := db.Connection.Exec(`CREATE TABLE launches
	(name TEXT, unique_id TEXT, ll_id INT, net_unix INT, status_id INT, status_state TEXT,
	in_hold BOOLEAN, probability REAL, success BOOLEAN, tbd_time BOOLEAN, tbd_date BOOLEAN,
	launched BOOLEAN,

	webcast_islive BOOLEAN, webcast_url_list TEXT,

	lsp_id INT, lsp_name TEXT, lsp_short TEXT, lsp_country_code TEXT,
	
	mission_name TEXT, mission_type TEXT, mission_orbit TEXT, mission_orbit_abbrev TEXT,
	mission_description TEXT,

	pad_name TEXT, location_name TEXT, location_country_code TEXT,

	rocket_name TEXT, rocket_full_name TEXT, rocket_variant TEXT, rocket_family TEXT,
	
	launcher_stage_id TEXT, launcher_stage_type TEXT, launcher_stage_is_reused BOOLEAN,
	launcher_stage_flight_number INT, launcher_stage_turn_around TEXT, launcher_is_flight_proven BOOLEAN,
	launcher_serial_number TEXT, launcher_maiden_flight INT, launcher_last_flight INT,
	launcher_landing_attempt BOOLEAN, launcher_landing_location TEXT, landing_type TEXT,
	launcher_landing_location_nth_landing INT,

	spacecraft_id INT, spacecraft_sn TEXT, spacecraft_name TEXT, spacecraft_crew TEXT,
	spacecraft_crew_count INT, spacecraft_maiden_flight INT,

	pad_nth_launch INT, location_nth_launch INT, agency_nth_launch INT, agency_nth_launch_year INT,
	orbital_nth_launch_year INT, 

	last_updated INT,

	notify_24h BOOLEAN, notify_12h BOOLEAN, notify_60min BOOLEAN, notify_5min BOOLEAN,

	muted_by TEXT, sent_notification_ids TEXT,
	PRIMARY KEY (unique_id))`)

	if err != nil {
		log.Fatal().Err(err).Msg("Error creating launches table")
	}

	// TODO: create indices once schema finished
}

/* Checks if table with name 'tableName' exists */
func tableExists(db *Database, tableName string) bool {
	rows, err := db.Connection.Query(`
	SELECT name FROM sqlite_master WHERE type = ? AND name = ?`, "table", tableName)

	if err != nil {
		log.Fatal().Err(err).Msg("Error executing query")
	}

	var rowName string

	// Read the row: there's only one, if there is any; no need to loop
	success := rows.Next()

	// rows.Next() may not return anything apart from the bool
	if !success {
		return false
	}

	// Scan rows
	err = rows.Scan(&rowName)

	if err != nil {
		log.Fatal().Err(err).Msg("Error reading table")
	}

	rows.Close()
	return rowName != ""
}

/* Verify that all expected tables exist in the db */
func (db *Database) verifyTablesExist() {
	// Tables that should be in the db
	tables := [3]string{
		"launches", "chats", "stats",
	}

	// Check if all tables exist, create if not
	for _, tableName := range tables {
		if !tableExists(db, tableName) {
			log.Info().Msgf("Table=%s does not exist: creating...", tableName)

			switch tableName {
			case "launches":
				db.createLaunchTable()
			case "chats":
				db.createChatsTable()
			case "stats":
				db.createStatsTable()
			}
		}
	}
}
