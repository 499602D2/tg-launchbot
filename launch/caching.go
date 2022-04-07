package launch

import (
	"sync"
	"time"
)

/* Caching.go implements a hot, in-memory launch cache for LaunchBot.
 */

type LaunchCache struct {
	Launches *[]Launch
	Updated  int64
	Mutex    sync.Mutex
}

func (cache *LaunchCache) Update(launches *[]Launch) {
	cache.Mutex.Lock()
	cache.Launches = launches
	cache.Updated = time.Now().Unix()
	cache.Mutex.Unlock()
}
