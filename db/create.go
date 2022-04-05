package db

import "github.com/rs/zerolog/log"

func (db *Database) createTables() {

}

func (db *Database) verifyTablesExist() {
	// Check if db has any tables
	rows, err := db.Connection.Query("SELECT name FROM sqlite_master WHERE type = table")

	if err != nil {
		log.Error().Err(err).Msg("Error querying database existence!")
	}

	defer rows.Close()

	var rowName string
	var rowNames []string

	// Tables we expect to find
	tables := [3]string{
		"launches", "chats", "statistics",
	}

	for rows.Next() {
		err := rows.Scan(&rowName)
		if err != nil {
			log.Error().Err(err).Msg("Error reading tables!")
		}

		rowNames = append(rowNames, rowName)
	}

	if len(rowNames) != len(tables) {
		db.createTables()
	} else {
		log.Info().Msg("Tables found!")
	}
}
