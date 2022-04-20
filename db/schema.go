package db

import (
	"time"

	"gorm.io/gorm"
)

type Chat struct {
	Id                   string `gorm:"primaryKey;index:enabled;index:disabled"`
	Platform             string `gorm:"primaryKey"`
	Locale               string // E.g. "Europe/Berlin"
	UtcOffset            float32
	Enabled24h           bool `gorm:"index:enabled;index:disabled"`
	Enabled12h           bool `gorm:"index:enabled;index:disabled"`
	Enabled1h            bool `gorm:"index:enabled;index:disabled"`
	Enabled5min          bool `gorm:"index:enabled;index:disabled"`
	SubscribedAll        bool `gorm:"index:enabled;index:disabled"`
	SubscribedNewsletter bool
	SubscribedTo         string    // List of LSP IDs
	UnsubscribedFrom     string    // List of LSP IDs
	Statistics           ChatStats `gorm:"embedded"`

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// Embedded chat-level statistics
type ChatStats struct {
	ReceivedNotifications int64
	SentCommands          int64
	MemberCount           int64
	SubscribedSince       int64
}

// Global statistics, per-platform
type Stats struct {
	Platform      string `gorm:"primaryKey;uniqueIndex"`
	Notifications int64
	Commands      int64
	ApiRequests   int64
	LastApiUpdate time.Time
	UpdatedAt     time.Time
}

// A launch row
type Launches struct {
	Launch    Launch `gorm:"embedded"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}
