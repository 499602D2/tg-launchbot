package utils

import (
	"fmt"
	"testing"
	"time"
)

func TestFriendlyETA(t *testing.T) {
	userNow := time.Now()

	// Test a date that's today
	eta := FriendlyETA(userNow, time.Second*2)
	fmt.Printf("Today ETA: %s\n\n", eta)

	// Test with a time that's tomorrow at 0:00
	userTomorrow := userNow.Add(time.Hour * 24)
	tmrY, tmrM, tmrD := userTomorrow.Date()
	tomorrow := time.Date(tmrY, tmrM, tmrD, 0, 0, 0, 0, time.Now().Location())
	eta = FriendlyETA(userNow, tomorrow.Sub(userNow))
	fmt.Printf("Tomorrow ETA: %s\n\n", eta)

	// Test with a time that's the day after tomorrow, at 0:00
	dayAfterTomorrow := tomorrow.Add(time.Hour * 24)
	eta = FriendlyETA(userNow, dayAfterTomorrow.Sub(userNow))
	fmt.Printf("Day after tomorrow ETA: %s\n\n", eta)

	// Test with a difference of exactly 1 day, at midnight
	tmrMidnight := time.Date(tmrY, tmrM, tmrD, 0, 0, 0, 0, time.Now().Location())
	eta = FriendlyETA(tmrMidnight, time.Hour*24)
	fmt.Printf("Midnight 24-hour ETA: %s\n\n", eta)

	// Test with one week
	eta = FriendlyETA(userNow, time.Hour*24*7)
	fmt.Printf("7-day ETA: %s\n\n", eta)

	// Test with one week
	eta = FriendlyETA(userNow, time.Hour*24+time.Hour*12)
	fmt.Printf("1 day + 12 hours ETA: %s\n\n", eta)
}
