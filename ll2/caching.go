package ll2

import (
	"sync"
	"time"
)

/* Caching.go implements a hot, in-memory launch cache for LaunchBot */

type LaunchCache struct {
	Launches map[string]*Launch // Maps the launch ID to the launch object
	Updated  int64              // Time the cache was last updated
	Mutex    sync.Mutex
}

func (cache *LaunchCache) Update(launches []*Launch) {
	cache.Mutex.Lock()
	defer cache.Mutex.Unlock()

	// Delete the old cache
	cache.Launches = make(map[string]*Launch)

	// Re-insert all launches into the map
	for _, launch := range launches {
		cache.Launches[launch.Id] = launch
	}

	cache.Updated = time.Now().Unix()
}

// Populates the cache from database
func (cache *LaunchCache) Populate() {
	cache.Mutex.Lock()
	defer cache.Mutex.Unlock()

	/* TODO implement
	- select all launches that have not launched
	- create a list of launch objects from the returned rows
	- do a cache.Update()
	*/

	// TODO load notification states from the database for all launches
	// (launch.Notifications)
}
