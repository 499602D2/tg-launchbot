package api

import "launchbot/launch"

/* Inserts the parsed launches into the database */
func updateLaunchDatabase(launches *[]launch.Launch) error {

	return nil
}

/* Get all launches that had their NET postponed following this update. */
func getPostponedLaunches() {}

/* Cleans launches from the DB that have slipped away from the request range.
This could be the result of the NET moving to the right, or the launch being
deleted. */
func cleanSlippedLaunches() {}

/*  */
func parseLaunchUpdate(update *launch.LL2LaunchUpdate) (*[]launch.Launch, error) {

	// Clean the launch database
	cleanSlippedLaunches()

	// Get and return all launches that were postponed
	getPostponedLaunches()

	return nil, nil
}
