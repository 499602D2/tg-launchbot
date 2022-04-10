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

	// Delete the old cache
	cache.Launches = make(map[string]*Launch)

	// Re-insert all launches into the map
	for _, launch := range launches {
		cache.Launches[launch.Id] = launch
	}

	cache.Updated = time.Now().Unix()
	cache.Mutex.Unlock()
}
