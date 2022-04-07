package db

/* Cleans launches from the DB that have slipped away from the request range.
This could be the result of the NET moving to the right, or the launch being
deleted. */
func (db *Database) CleanSlippedLaunches() error {
	// Clean all launches that have launched = 0 and weren't updated in the last update

	/*
		# Select all launches
		cursor.execute(
		'SELECT unique_id FROM launches WHERE launched = 0 AND last_updated < ? AND net_unix > ?',
		(last_update, int(time.time())))

		deleted_launches = set()
		for launch_row in cursor.fetchall():
			deleted_launches.add(launch_row[0])

		# If no rows returned, nothing to do
		if len(deleted_launches) == 0:
			logging.debug('✨ Database already clean: nothing to do!')
			return

		# More than one launch out of range
		logging.info(
		f'✨ Deleting {len(deleted_launches)} launches that have slipped out of range...'
		)

		cursor.execute(
			'DELETE FROM launches WHERE launched = 0 AND last_updated < ? AND net_unix > ?',
			(last_update, int(time.time())))
	*/
	return nil
}
