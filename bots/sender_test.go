package bots

import (
	"fmt"
	"launchbot/sendables"
	"launchbot/users"
	"testing"
	"time"
)

func TestSetTime(t *testing.T) {
	txt := "$USERDATE"
	msg := sendables.Message{
		TextContent: txt, AddUserTime: true, RefTime: time.Now().Unix(),
	}

	// Positive offset
	user1 := users.User{}
	user1.SetTimeZone()
	newText := sendables.SetTime(msg.TextContent, &user1, msg.RefTime, false, false)
	fmt.Println(newText)
}
