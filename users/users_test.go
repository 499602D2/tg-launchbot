package users

import (
	"testing"
)

func TestAddBlockedKeyword(t *testing.T) {
	user := &User{}
	
	// Test adding first keyword
	if !user.AddBlockedKeyword("Starlink") {
		t.Error("Failed to add first blocked keyword")
	}
	if user.BlockedKeywords != "Starlink" {
		t.Errorf("Expected BlockedKeywords to be 'Starlink', got '%s'", user.BlockedKeywords)
	}
	
	// Test adding second keyword
	if !user.AddBlockedKeyword("OneWeb") {
		t.Error("Failed to add second blocked keyword")
	}
	if user.BlockedKeywords != "Starlink,OneWeb" {
		t.Errorf("Expected BlockedKeywords to be 'Starlink,OneWeb', got '%s'", user.BlockedKeywords)
	}
	
	// Test adding duplicate keyword
	if user.AddBlockedKeyword("Starlink") {
		t.Error("Should not add duplicate keyword")
	}
	
	// Test case-insensitive duplicate check
	if user.AddBlockedKeyword("starlink") {
		t.Error("Should not add case-insensitive duplicate keyword")
	}
}

func TestRemoveBlockedKeyword(t *testing.T) {
	user := &User{BlockedKeywords: "Starlink,OneWeb,Falcon"}
	
	// Test removing middle keyword
	if !user.RemoveBlockedKeyword("OneWeb") {
		t.Error("Failed to remove keyword")
	}
	if user.BlockedKeywords != "Starlink,Falcon" {
		t.Errorf("Expected BlockedKeywords to be 'Starlink,Falcon', got '%s'", user.BlockedKeywords)
	}
	
	// Test removing first keyword
	if !user.RemoveBlockedKeyword("Starlink") {
		t.Error("Failed to remove first keyword")
	}
	if user.BlockedKeywords != "Falcon" {
		t.Errorf("Expected BlockedKeywords to be 'Falcon', got '%s'", user.BlockedKeywords)
	}
	
	// Test removing last keyword
	if !user.RemoveBlockedKeyword("Falcon") {
		t.Error("Failed to remove last keyword")
	}
	if user.BlockedKeywords != "" {
		t.Errorf("Expected BlockedKeywords to be empty, got '%s'", user.BlockedKeywords)
	}
	
	// Test removing non-existent keyword
	if user.RemoveBlockedKeyword("NotThere") {
		t.Error("Should not remove non-existent keyword")
	}
	
	// Test case-insensitive removal
	user.BlockedKeywords = "Starlink"
	if !user.RemoveBlockedKeyword("STARLINK") {
		t.Error("Failed to remove keyword with different case")
	}
}

func TestHasBlockedKeyword(t *testing.T) {
	user := &User{BlockedKeywords: "Starlink,OneWeb,Falcon"}
	
	// Test existing keywords
	if !user.HasBlockedKeyword("Starlink") {
		t.Error("Should find Starlink")
	}
	if !user.HasBlockedKeyword("OneWeb") {
		t.Error("Should find OneWeb")
	}
	if !user.HasBlockedKeyword("Falcon") {
		t.Error("Should find Falcon")
	}
	
	// Test case-insensitive check
	if !user.HasBlockedKeyword("starlink") {
		t.Error("Should find starlink (case-insensitive)")
	}
	if !user.HasBlockedKeyword("ONEWEB") {
		t.Error("Should find ONEWEB (case-insensitive)")
	}
	
	// Test non-existent keyword
	if user.HasBlockedKeyword("Dragon") {
		t.Error("Should not find Dragon")
	}
	
	// Test empty keywords
	user.BlockedKeywords = ""
	if user.HasBlockedKeyword("Anything") {
		t.Error("Should not find anything when keywords are empty")
	}
}

func TestAllowedKeywordFunctions(t *testing.T) {
	user := &User{}
	
	// Test add
	if !user.AddAllowedKeyword("ISS") {
		t.Error("Failed to add allowed keyword")
	}
	if user.AllowedKeywords != "ISS" {
		t.Errorf("Expected AllowedKeywords to be 'ISS', got '%s'", user.AllowedKeywords)
	}
	
	// Test has
	if !user.HasAllowedKeyword("ISS") {
		t.Error("Should find ISS")
	}
	if !user.HasAllowedKeyword("iss") {
		t.Error("Should find iss (case-insensitive)")
	}
	
	// Test remove
	if !user.RemoveAllowedKeyword("ISS") {
		t.Error("Failed to remove allowed keyword")
	}
	if user.AllowedKeywords != "" {
		t.Errorf("Expected AllowedKeywords to be empty, got '%s'", user.AllowedKeywords)
	}
}


func TestToggleLaunchMute(t *testing.T) {
	// Create a test user
	user := &User{
		Id:            "123",
		Platform:      "test",
		MutedLaunches: "",
	}

	launchID := "test-launch-123"

	// Test muting a launch
	t.Run("Mute unmuted launch", func(t *testing.T) {
		result := user.ToggleLaunchMute(launchID, true)
		if !result {
			t.Error("Expected true when muting unmuted launch")
		}
		if !user.HasMutedLaunch(launchID) {
			t.Error("Launch should be muted")
		}
	})

	// Test muting already muted launch
	t.Run("Mute already muted launch", func(t *testing.T) {
		result := user.ToggleLaunchMute(launchID, true)
		if result {
			t.Error("Expected false when muting already muted launch")
		}
		if !user.HasMutedLaunch(launchID) {
			t.Error("Launch should still be muted")
		}
	})

	// Test unmuting a muted launch
	t.Run("Unmute muted launch", func(t *testing.T) {
		result := user.ToggleLaunchMute(launchID, false)
		if !result {
			t.Error("Expected true when unmuting muted launch")
		}
		if user.HasMutedLaunch(launchID) {
			t.Error("Launch should be unmuted")
		}
	})

	// Test unmuting already unmuted launch
	t.Run("Unmute already unmuted launch", func(t *testing.T) {
		result := user.ToggleLaunchMute(launchID, false)
		if result {
			t.Error("Expected false when unmuting already unmuted launch")
		}
		if user.HasMutedLaunch(launchID) {
			t.Error("Launch should still be unmuted")
		}
	})

	// Test with multiple launches
	t.Run("Multiple launches", func(t *testing.T) {
		launch1 := "launch-1"
		launch2 := "launch-2"
		launch3 := "launch-3"

		// Mute multiple launches
		user.ToggleLaunchMute(launch1, true)
		user.ToggleLaunchMute(launch2, true)
		user.ToggleLaunchMute(launch3, true)

		if !user.HasMutedLaunch(launch1) || !user.HasMutedLaunch(launch2) || !user.HasMutedLaunch(launch3) {
			t.Error("All launches should be muted")
		}

		// Unmute middle launch
		user.ToggleLaunchMute(launch2, false)
		if !user.HasMutedLaunch(launch1) || user.HasMutedLaunch(launch2) || !user.HasMutedLaunch(launch3) {
			t.Error("Only launch2 should be unmuted")
		}
	})
}

func TestShouldReceiveLaunch(t *testing.T) {
	tests := []struct {
		name          string
		user          User
		launchId      string
		providerId    int
		launchName    string
		vehicleName   string
		missionName   string
		expectedResult bool
	}{
		// Test muted launch
		{
			name:          "Muted launch",
			user:          User{MutedLaunches: "launch-123", SubscribedAll: true},
			launchId:      "launch-123",
			providerId:    1,
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: false,
		},
		// Test blocked keyword (always excludes)
		{
			name:          "Blocked keyword",
			user:          User{BlockedKeywords: "Starlink", SubscribedAll: true},
			launchId:      "launch-456",
			providerId:    1,
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: false,
		},
		// Test allowed keyword (always includes)
		{
			name:          "Allowed keyword overrides provider subscription",
			user:          User{AllowedKeywords: "ISS", SubscribedAll: false},
			launchId:      "launch-789",
			providerId:    99, // Not subscribed to this provider
			launchName:    "Crew-11",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Mission",
			expectedResult: true,
		},
		// Test blocked keyword takes precedence over allowed
		{
			name:          "Blocked keyword takes precedence",
			user:          User{BlockedKeywords: "Crew", AllowedKeywords: "ISS"},
			launchId:      "launch-789",
			providerId:    99,
			launchName:    "Crew-11",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Mission",
			expectedResult: false,
		},
		// Test no keywords - follows provider subscription
		{
			name:          "No keywords - not subscribed to provider",
			user:          User{SubscribedAll: false},
			launchId:      "launch-123",
			providerId:    99,
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: false,
		},
		// Test no keywords - subscribed to provider
		{
			name:          "No keywords - subscribed to provider",
			user:          User{SubscribedAll: true},
			launchId:      "launch-123",
			providerId:    1,
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: true,
		},
		// Test multiple blocked keywords
		{
			name:          "Multiple blocked keywords",
			user:          User{BlockedKeywords: "OneWeb,Starlink,Iridium", SubscribedAll: true},
			launchId:      "launch-456",
			providerId:    1,
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: false,
		},
		// Test multiple allowed keywords
		{
			name:          "Multiple allowed keywords",
			user:          User{AllowedKeywords: "ISS,Dragon,Crew", SubscribedAll: false},
			launchId:      "launch-789",
			providerId:    99,
			launchName:    "Crew-5 Mission",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Crew Rotation",
			expectedResult: true,
		},
		// Test case insensitive matching
		{
			name:          "Case insensitive keyword matching",
			user:          User{BlockedKeywords: "starlink", SubscribedAll: true},
			launchId:      "launch-456",
			providerId:    1,
			launchName:    "STARLINK Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: false,
		},
		// Test partial matching
		{
			name:          "Partial keyword matching",
			user:          User{AllowedKeywords: "Star", SubscribedAll: false},
			launchId:      "launch-456",
			providerId:    99,
			launchName:    "Starship Test Flight",
			vehicleName:   "Starship",
			missionName:   "Test",
			expectedResult: true,
		},
		// Test keyword with spaces (trimmed)
		{
			name:          "Keywords with spaces are trimmed",
			user:          User{BlockedKeywords: " Starlink , OneWeb ", SubscribedAll: true},
			launchId:      "launch-456",
			providerId:    1,
			launchName:    "OneWeb Mission",
			vehicleName:   "Soyuz",
			missionName:   "Communications",
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.user.ShouldReceiveLaunch(tt.launchId, tt.providerId, tt.launchName, tt.vehicleName, tt.missionName)
			if result != tt.expectedResult {
				t.Errorf("Expected %v, got %v", tt.expectedResult, result)
			}
		})
	}
}