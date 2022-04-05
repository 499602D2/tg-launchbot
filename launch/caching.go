package launch

import (
	"sync"
	"time"
)

/* Caching.go implements a hot, in-memory launch cache for LaunchBot.
 */

type LaunchCache struct {
	Launches *[]Launch
	Updated  time.Time
	Mutex    sync.Mutex
}

func (cache *LaunchCache) Update(launches *[]Launch) {
	cache.Mutex.Lock()
	cache.Launches = launches
	cache.Updated = time.Now()
	cache.Mutex.Unlock()
}
