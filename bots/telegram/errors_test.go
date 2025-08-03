package telegram

import (
	"errors"
	"strings"
	"testing"

	tb "gopkg.in/telebot.v3"
)

func TestCustomErrorDefinitions(t *testing.T) {
	// Test that custom errors are properly defined
	if ErrGroupDeleted == nil {
		t.Error("ErrGroupDeleted is nil")
	}
	if ErrGroupDeleted2 == nil {
		t.Error("ErrGroupDeleted2 is nil")
	}
	if ErrNoRightsToSendText == nil {
		t.Error("ErrNoRightsToSendText is nil")
	}
	if ErrNoRightsToSendText2 == nil {
		t.Error("ErrNoRightsToSendText2 is nil")
	}

	// Test error messages
	if !strings.Contains(ErrGroupDeleted.Error(), "the group chat was deleted") {
		t.Errorf("ErrGroupDeleted error message incorrect: %s", ErrGroupDeleted.Error())
	}
	if !strings.Contains(ErrGroupDeleted2.Error(), "the group chat was deleted") {
		t.Errorf("ErrGroupDeleted2 error message incorrect: %s", ErrGroupDeleted2.Error())
	}
	if !strings.Contains(ErrNoRightsToSendText.Error(), "not enough rights to send text messages to the chat") {
		t.Errorf("ErrNoRightsToSendText error message incorrect: %s", ErrNoRightsToSendText.Error())
	}
	if !strings.Contains(ErrNoRightsToSendText2.Error(), "not enough rights to send text messages to the chat") {
		t.Errorf("ErrNoRightsToSendText2 error message incorrect: %s", ErrNoRightsToSendText2.Error())
	}

	// Test error codes
	if ErrGroupDeleted.Code != 403 {
		t.Errorf("ErrGroupDeleted code = %d, want 403", ErrGroupDeleted.Code)
	}
	if ErrGroupDeleted2.Code != 403 {
		t.Errorf("ErrGroupDeleted2 code = %d, want 403", ErrGroupDeleted2.Code)
	}
	if ErrNoRightsToSendText.Code != 400 {
		t.Errorf("ErrNoRightsToSendText code = %d, want 400", ErrNoRightsToSendText.Code)
	}
	if ErrNoRightsToSendText2.Code != 400 {
		t.Errorf("ErrNoRightsToSendText2 code = %d, want 400", ErrNoRightsToSendText2.Code)
	}
}

func TestErrorMatching(t *testing.T) {
	// Test direct error matching
	if ErrGroupDeleted != ErrGroupDeleted {
		t.Error("ErrGroupDeleted should equal itself")
	}
	if ErrGroupDeleted2 != ErrGroupDeleted2 {
		t.Error("ErrGroupDeleted2 should equal itself")
	}
	
	// Test that our custom errors match the expected patterns
	testCases := []struct {
		name     string
		err      error
		contains string
	}{
		{
			name:     "ErrGroupDeleted matches pattern",
			err:      ErrGroupDeleted,
			contains: "the group chat was deleted",
		},
		{
			name:     "ErrGroupDeleted2 matches pattern",
			err:      ErrGroupDeleted2,
			contains: "the group chat was deleted",
		},
		{
			name:     "Random error with same text matches pattern",
			err:      errors.New("telegram: Forbidden: the group chat was deleted"),
			contains: "Forbidden: the group chat was deleted",
		},
		{
			name:     "Wrapped error matches pattern",
			err:      errors.New("API Error: Forbidden: the group chat was deleted (403)"),
			contains: "Forbidden: the group chat was deleted",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.err.Error(), tc.contains) {
				t.Errorf("Error %q does not contain %q", tc.err.Error(), tc.contains)
			}
		})
	}
}

func TestTelebotErrorCodes(t *testing.T) {
	// Verify Telebot errors we depend on have the expected codes
	if tb.ErrBlockedByUser.Code != 403 {
		t.Errorf("tb.ErrBlockedByUser code = %d, want 403", tb.ErrBlockedByUser.Code)
	}
	if tb.ErrKickedFromGroup.Code != 403 {
		t.Errorf("tb.ErrKickedFromGroup code = %d, want 403", tb.ErrKickedFromGroup.Code)
	}
	if tb.ErrKickedFromSuperGroup.Code != 403 {
		t.Errorf("tb.ErrKickedFromSuperGroup code = %d, want 403", tb.ErrKickedFromSuperGroup.Code)
	}
	if tb.ErrNotStartedByUser.Code != 403 {
		t.Errorf("tb.ErrNotStartedByUser code = %d, want 403", tb.ErrNotStartedByUser.Code)
	}
	if tb.ErrUserIsDeactivated.Code != 403 {
		t.Errorf("tb.ErrUserIsDeactivated code = %d, want 403", tb.ErrUserIsDeactivated.Code)
	}
	if tb.ErrChatNotFound.Code != 400 {
		t.Errorf("tb.ErrChatNotFound code = %d, want 400", tb.ErrChatNotFound.Code)
	}
}