package bots

import (
	"fmt"
	"launchbot/users"
	"testing"
	"time"
)

func TestSetTime(t *testing.T) {
	txt := "$USERTIME"
	msg := Message{
		TextContent: &txt, AddUserTime: true, RefTime: time.Now().Unix(),
	}

	// Positive offset
	loc, _ := time.LoadLocation("Europe/Berlin")
	user1 := users.User{TimeZone: loc}
	newText := msg.SetTime(&user1)
	fmt.Println(*newText)

	// Local offset
	loc, _ = time.LoadLocation("Europe/Helsinki")
	user1 = users.User{TimeZone: loc}
	newText = msg.SetTime(&user1)
	fmt.Println(*newText)

	// Negative offset
	loc, _ = time.LoadLocation("America/Los_Angeles")
	user1 = users.User{TimeZone: loc}
	newText = msg.SetTime(&user1)
	fmt.Println(*newText)

	// Non-integer offset
	loc, _ = time.LoadLocation("Australia/Eucla")
	user1 = users.User{TimeZone: loc}
	newText = msg.SetTime(&user1)
	fmt.Println(*newText)

	// UTC
	loc, _ = time.LoadLocation("UTC")
	user1 = users.User{TimeZone: loc}
	newText = msg.SetTime(&user1)
	fmt.Println(*newText)

}
