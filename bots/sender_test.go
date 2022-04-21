package bots

import (
	"fmt"
	"launchbot/sendables"
	"launchbot/users"
	"testing"
	"time"
)

func TestSetTime(t *testing.T) {
	txt := "$USERTIME"
	msg := sendables.Message{
		TextContent: &txt, AddUserTime: true, RefTime: time.Now().Unix(),
	}

	// Positive offset
	user1 := users.User{}
	user1.SetTimeZone()
	newText := msg.SetTime(&user1)
	fmt.Println(*newText)

	// Local offset
	// "Europe/Helsinki"

	// Negative offset
	// "America/Los_Angeles"

	// Non-integer offset
	// "Australia/Eucla"

	// UTC
	// "UTC"

}
