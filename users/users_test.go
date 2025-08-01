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