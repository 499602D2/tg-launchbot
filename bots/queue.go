package bots

import (
	"launchbot/sendables"
	"sync"

	"github.com/rs/zerolog/log"
	uuid "github.com/satori/go.uuid"
)

// A queue of sendables to be sent
type Queue struct {
	Sendables    map[string]*sendables.Sendable // Sendable queue (maps make removals easy)
	HighPriority *HighPriorityQueue             // High-priority queue
	Mutex        sync.Mutex                     // Mutex to avoid concurrent writes
}

// A high-priority queue, meant for individual messages, that is cleared periodically
type HighPriorityQueue struct {
	HasItemsInQueue bool
	Queue           []*sendables.Sendable // A slice is adequate here, as it's always emptied completely
	Mutex           sync.Mutex
}

// Enqueues a message into a queue
func (queue *Queue) Enqueue(sendable *sendables.Sendable, highPriority bool) {
	// Calculate size and set token count
	sendable.Size = sendable.PerceivedByteSize()
	sendable.Tokens = 1

	if sendable.Size >= 512 && !highPriority {
		sendable.Tokens = 6
		log.Debug().Msgf("Reserved %d token(s) for sendable, size=%d", sendable.Tokens, sendable.Size)
	}

	// If sendable is high-priority, add it to the high-priority queue
	if highPriority {
		queue.HighPriority.Mutex.Lock()

		// Mark queue as having items, append sendable to queue
		queue.HighPriority.HasItemsInQueue = true
		queue.HighPriority.Queue = append(queue.HighPriority.Queue, sendable)

		queue.HighPriority.Mutex.Unlock()
		return
	}

	// Assign a random hash to the sendable, enqueue it
	queue.Mutex.Lock()
	queue.Sendables[uuid.NewV4().String()] = sendable
	queue.Mutex.Unlock()
}
