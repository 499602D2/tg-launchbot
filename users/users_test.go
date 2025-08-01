package users

import (
	"testing"
)

func TestAddMutedKeyword(t *testing.T) {
	user := &User{}
	
	// Test adding first keyword
	if !user.AddMutedKeyword("Starlink") {
		t.Error("Failed to add first muted keyword")
	}
	if user.MutedKeywords != "Starlink" {
		t.Errorf("Expected MutedKeywords to be 'Starlink', got '%s'", user.MutedKeywords)
	}
	
	// Test adding second keyword
	if !user.AddMutedKeyword("OneWeb") {
		t.Error("Failed to add second muted keyword")
	}
	if user.MutedKeywords != "Starlink,OneWeb" {
		t.Errorf("Expected MutedKeywords to be 'Starlink,OneWeb', got '%s'", user.MutedKeywords)
	}
	
	// Test adding duplicate keyword
	if user.AddMutedKeyword("Starlink") {
		t.Error("Should not add duplicate keyword")
	}
	
	// Test case-insensitive duplicate check
	if user.AddMutedKeyword("starlink") {
		t.Error("Should not add case-insensitive duplicate keyword")
	}
}

func TestRemoveMutedKeyword(t *testing.T) {
	user := &User{MutedKeywords: "Starlink,OneWeb,Falcon"}
	
	// Test removing middle keyword
	if !user.RemoveMutedKeyword("OneWeb") {
		t.Error("Failed to remove keyword")
	}
	if user.MutedKeywords != "Starlink,Falcon" {
		t.Errorf("Expected MutedKeywords to be 'Starlink,Falcon', got '%s'", user.MutedKeywords)
	}
	
	// Test removing first keyword
	if !user.RemoveMutedKeyword("Starlink") {
		t.Error("Failed to remove first keyword")
	}
	if user.MutedKeywords != "Falcon" {
		t.Errorf("Expected MutedKeywords to be 'Falcon', got '%s'", user.MutedKeywords)
	}
	
	// Test removing last keyword
	if !user.RemoveMutedKeyword("Falcon") {
		t.Error("Failed to remove last keyword")
	}
	if user.MutedKeywords != "" {
		t.Errorf("Expected MutedKeywords to be empty, got '%s'", user.MutedKeywords)
	}
	
	// Test removing non-existent keyword
	if user.RemoveMutedKeyword("NotThere") {
		t.Error("Should not remove non-existent keyword")
	}
	
	// Test case-insensitive removal
	user.MutedKeywords = "Starlink"
	if !user.RemoveMutedKeyword("STARLINK") {
		t.Error("Failed to remove keyword with different case")
	}
}

func TestHasMutedKeyword(t *testing.T) {
	user := &User{MutedKeywords: "Starlink,OneWeb,Falcon"}
	
	// Test existing keywords
	if !user.HasMutedKeyword("Starlink") {
		t.Error("Should find Starlink")
	}
	if !user.HasMutedKeyword("OneWeb") {
		t.Error("Should find OneWeb")
	}
	if !user.HasMutedKeyword("Falcon") {
		t.Error("Should find Falcon")
	}
	
	// Test case-insensitive check
	if !user.HasMutedKeyword("starlink") {
		t.Error("Should find starlink (case-insensitive)")
	}
	if !user.HasMutedKeyword("ONEWEB") {
		t.Error("Should find ONEWEB (case-insensitive)")
	}
	
	// Test non-existent keyword
	if user.HasMutedKeyword("Dragon") {
		t.Error("Should not find Dragon")
	}
	
	// Test empty keywords
	user.MutedKeywords = ""
	if user.HasMutedKeyword("Anything") {
		t.Error("Should not find anything when keywords are empty")
	}
}

func TestSubscribedKeywordFunctions(t *testing.T) {
	user := &User{}
	
	// Test add
	if !user.AddSubscribedKeyword("ISS") {
		t.Error("Failed to add subscribed keyword")
	}
	if user.SubscribedKeywords != "ISS" {
		t.Errorf("Expected SubscribedKeywords to be 'ISS', got '%s'", user.SubscribedKeywords)
	}
	
	// Test has
	if !user.HasSubscribedKeyword("ISS") {
		t.Error("Should find ISS")
	}
	if !user.HasSubscribedKeyword("iss") {
		t.Error("Should find iss (case-insensitive)")
	}
	
	// Test remove
	if !user.RemoveSubscribedKeyword("ISS") {
		t.Error("Failed to remove subscribed keyword")
	}
	if user.SubscribedKeywords != "" {
		t.Errorf("Expected SubscribedKeywords to be empty, got '%s'", user.SubscribedKeywords)
	}
}

func TestMatchesKeywordFilter(t *testing.T) {
	tests := []struct {
		name               string
		user               User
		launchName         string
		vehicleName        string
		missionName        string
		expectedMatch      bool
	}{
		// Exclude mode tests
		{
			name:          "Exclude mode - no keywords",
			user:          User{FilterMode: "exclude"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		{
			name:          "Exclude mode - match muted keyword",
			user:          User{FilterMode: "exclude", MutedKeywords: "Starlink"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		{
			name:          "Exclude mode - no match",
			user:          User{FilterMode: "exclude", MutedKeywords: "OneWeb"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		{
			name:          "Exclude mode - partial match",
			user:          User{FilterMode: "exclude", MutedKeywords: "Star"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		{
			name:          "Exclude mode - case insensitive",
			user:          User{FilterMode: "exclude", MutedKeywords: "starlink"},
			launchName:    "STARLINK Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		
		// Include mode tests
		{
			name:          "Include mode - no keywords",
			user:          User{FilterMode: "include"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		{
			name:          "Include mode - match subscribed keyword",
			user:          User{FilterMode: "include", SubscribedKeywords: "Starlink"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		{
			name:          "Include mode - no match",
			user:          User{FilterMode: "include", SubscribedKeywords: "ISS"},
			launchName:    "Starlink Group 6-23",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		{
			name:          "Include mode - match in vehicle",
			user:          User{FilterMode: "include", SubscribedKeywords: "Falcon"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		{
			name:          "Include mode - match in mission",
			user:          User{FilterMode: "include", SubscribedKeywords: "Communications"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications Satellite",
			expectedMatch: true,
		},
		
		// Hybrid mode tests
		{
			name:          "Hybrid mode - match subscribed, not muted",
			user:          User{FilterMode: "hybrid", SubscribedKeywords: "Falcon", MutedKeywords: "OneWeb"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		{
			name:          "Hybrid mode - match subscribed and muted",
			user:          User{FilterMode: "hybrid", SubscribedKeywords: "Falcon", MutedKeywords: "Starlink"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		{
			name:          "Hybrid mode - no subscribed match",
			user:          User{FilterMode: "hybrid", SubscribedKeywords: "ISS", MutedKeywords: "Starlink"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		{
			name:          "Hybrid mode - no keywords",
			user:          User{FilterMode: "hybrid"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		
		// Default/empty filter mode
		{
			name:          "Default mode - should behave as exclude",
			user:          User{MutedKeywords: "Starlink"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		
		// Multiple keywords
		{
			name:          "Multiple muted keywords",
			user:          User{FilterMode: "exclude", MutedKeywords: "OneWeb,Starlink,Iridium"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		{
			name:          "Multiple subscribed keywords",
			user:          User{FilterMode: "include", SubscribedKeywords: "ISS,Dragon,Crew"},
			launchName:    "Crew-5 Mission",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Crew Rotation",
			expectedMatch: true,
		},
		
		// keywords_filter mode tests (same as exclude mode)
		{
			name:          "keywords_filter mode - no keywords",
			user:          User{FilterMode: "keywords_filter"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		{
			name:          "keywords_filter mode - match muted keyword",
			user:          User{FilterMode: "keywords_filter", MutedKeywords: "Starlink"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		{
			name:          "keywords_filter mode - no match",
			user:          User{FilterMode: "keywords_filter", MutedKeywords: "OneWeb"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		
		// keywords_add mode tests
		{
			name:          "keywords_add mode - no muted keywords",
			user:          User{FilterMode: "keywords_add"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: true,
		},
		{
			name:          "keywords_add mode - match muted keyword",
			user:          User{FilterMode: "keywords_add", MutedKeywords: "Starlink"},
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedMatch: false,
		},
		{
			name:          "keywords_add mode - multiple keywords with spaces",
			user:          User{FilterMode: "keywords_add", MutedKeywords: " Starlink , OneWeb "},
			launchName:    "OneWeb Mission",
			vehicleName:   "Soyuz",
			missionName:   "Communications",
			expectedMatch: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.user.MatchesKeywordFilter(tt.launchName, tt.vehicleName, tt.missionName)
			if result != tt.expectedMatch {
				t.Errorf("Expected %v, got %v", tt.expectedMatch, result)
			}
		})
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
		// Test muted keyword
		{
			name:          "Muted keyword",
			user:          User{MutedKeywords: "Starlink", SubscribedAll: true},
			launchId:      "launch-456",
			providerId:    1,
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: false,
		},
		// Test keywords_add mode - provider subscription
		{
			name:          "keywords_add mode - subscribed via provider",
			user:          User{FilterMode: "keywords_add", SubscribedAll: true},
			launchId:      "launch-789",
			providerId:    1,
			launchName:    "Crew-11",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Mission",
			expectedResult: true,
		},
		// Test keywords_add mode - keyword subscription
		{
			name:          "keywords_add mode - subscribed via keyword only",
			user:          User{FilterMode: "keywords_add", SubscribedKeywords: "ISS", SubscribedAll: false},
			launchId:      "launch-789",
			providerId:    99, // Not subscribed to this provider
			launchName:    "Crew-11",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Mission",
			expectedResult: true,
		},
		// Test keywords_add mode - neither provider nor keyword
		{
			name:          "keywords_add mode - no subscription match",
			user:          User{FilterMode: "keywords_add", SubscribedKeywords: "Mars", SubscribedAll: false},
			launchId:      "launch-789",
			providerId:    99, // Not subscribed to this provider
			launchName:    "Crew-11",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Mission",
			expectedResult: false,
		},
		// Test keywords_add mode - keyword match but muted
		{
			name:          "keywords_add mode - keyword match but muted",
			user:          User{FilterMode: "keywords_add", SubscribedKeywords: "ISS", MutedKeywords: "Crew"},
			launchId:      "launch-789",
			providerId:    99,
			launchName:    "Crew-11",
			vehicleName:   "Falcon 9",
			missionName:   "ISS Mission",
			expectedResult: false,
		},
		// Test legacy mode - must be subscribed to provider
		{
			name:          "Legacy mode - not subscribed to provider",
			user:          User{FilterMode: "exclude", SubscribedAll: false},
			launchId:      "launch-123",
			providerId:    99,
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: false,
		},
		// Test legacy mode - subscribed and passes filter
		{
			name:          "Legacy mode - subscribed and passes filter",
			user:          User{FilterMode: "exclude", SubscribedAll: true, MutedKeywords: "OneWeb"},
			launchId:      "launch-123",
			providerId:    1,
			launchName:    "Starlink Mission",
			vehicleName:   "Falcon 9",
			missionName:   "Communications",
			expectedResult: true,
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