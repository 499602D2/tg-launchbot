package templates

/*
	This module separates the ugly, space-intensive button generation code into its own file,
	which greatly improves the legibility and density of the code in the telegram.go file.
*/

import (
	"fmt"
	"launchbot/db"
	"launchbot/users"
	"launchbot/utils"
	"strings"

	"github.com/rs/zerolog/log"
	tb "gopkg.in/telebot.v3"
)

// Keyboard template wrapper
type Keyboard struct {
	Settings SettingsKeyboard
	Command  CommandKeyboard
}

// Settings sub-sections
type SettingsKeyboard struct {
	TimeZone     TimeZoneKeyboard
	Subscription SubscriptionKeyboard
	Keywords     KeywordsKeyboard
}

// Extend Settings{} with time-zone settings
type TimeZoneKeyboard struct {
}

// Extend Settings{} with subscription settings
type SubscriptionKeyboard struct {
}

// Extend Settings{} with keyword filter settings
type KeywordsKeyboard struct {
}

// Command templates
type CommandKeyboard struct {
}

func (settings *SettingsKeyboard) Main(isGroup bool) (tb.SendOptions, [][]tb.InlineButton) {
	subscribeBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "🚀 Subscribe to launches",
		Data:   "sub/bycountry",
	}

	keywordBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "🔍 Keyword Filters",
		Data:   "keywords/main",
	}

	timesBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "⏰ Adjust notifications",
		Data:   "sub/times",
	}

	tzBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "🌍 Time zone settings",
		Data:   "tz/main",
	}

	// Construct the keyboard and send-options
	kb := [][]tb.InlineButton{{subscribeBtn}, {keywordBtn}, {timesBtn}, {tzBtn}}

	// If chat is a group, show the group-specific settings
	if isGroup {
		groupSettingsBtn := tb.InlineButton{
			Unique: "settings",
			Text:   "👷 Group settings",
			Data:   "group/main",
		}

		// Add the extra key and the extra text
		kb = append(kb, []tb.InlineButton{groupSettingsBtn})
	}

	// Create send-options
	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (settings *SettingsKeyboard) Group(chat *users.User) (tb.SendOptions, [][]tb.InlineButton) {
	// Map status of current command access to a button label
	label := map[bool]string{
		true:  "🔇 Disable user commands",
		false: "📬 Enable user commands",
	}[chat.AnyoneCanSendCommands]

	toggleAllCmdAccessBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   label,
		Data:   fmt.Sprintf("cmd/all/%s", utils.ToggleBoolStateAsString[chat.AnyoneCanSendCommands]),
	}

	retBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "⬅️ Back to settings",
		Data:   "main",
	}

	kb := [][]tb.InlineButton{{toggleAllCmdAccessBtn}, {retBtn}}

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (settings *SettingsKeyboard) Notifications(chat *users.User) (tb.SendOptions, [][]tb.InlineButton) {
	time24hBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   fmt.Sprintf("%s 24-hour", utils.BoolStateIndicator[chat.Enabled24h]),
		Data:   fmt.Sprintf("time/24h/%s", utils.ToggleBoolStateAsString[chat.Enabled24h]),
	}

	time12hBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   fmt.Sprintf("%s 12-hour", utils.BoolStateIndicator[chat.Enabled12h]),
		Data:   fmt.Sprintf("time/12h/%s", utils.ToggleBoolStateAsString[chat.Enabled12h]),
	}

	time1hBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   fmt.Sprintf("%s 1-hour", utils.BoolStateIndicator[chat.Enabled1h]),
		Data:   fmt.Sprintf("time/1h/%s", utils.ToggleBoolStateAsString[chat.Enabled1h]),
	}

	time5minBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   fmt.Sprintf("%s 5-minute", utils.BoolStateIndicator[chat.Enabled5min]),
		Data:   fmt.Sprintf("time/5min/%s", utils.ToggleBoolStateAsString[chat.Enabled5min]),
	}

	postponeBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   fmt.Sprintf("%s Postponements", utils.BoolStateIndicator[chat.EnabledPostpone]),
		Data:   fmt.Sprintf("time/postpone/%s", utils.ToggleBoolStateAsString[chat.EnabledPostpone]),
	}

	retBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "⬅️ Return",
		Data:   "main",
	}

	// Keyboard
	kb := [][]tb.InlineButton{{time24hBtn, time12hBtn}, {time1hBtn, time5minBtn}, {postponeBtn}, {retBtn}}

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (tz *TimeZoneKeyboard) Main() (tb.SendOptions, [][]tb.InlineButton) {
	// Construct the keyboard and send-options
	setBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "🌍 Begin time zone set-up",
		Data:   "tz/begin",
	}

	delBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "❌ Delete your time zone",
		Data:   "tz/del",
	}

	retBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "⬅️ Back to settings",
		Data:   "main",
	}

	kb := [][]tb.InlineButton{{setBtn}, {delBtn}, {retBtn}}

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (tz *TimeZoneKeyboard) Setup() (tb.SendOptions, [][]tb.InlineButton) {
	kb := [][]tb.InlineButton{{
		tb.InlineButton{
			Unique: "settings",
			Text:   "⬅️ Cancel set-up",
			Data:   "tz/main",
		}},
	}

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (tz *TimeZoneKeyboard) Deleted() (tb.SendOptions, [][]tb.InlineButton) {
	retBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "⬅️ Back to settings",
		Data:   "main",
	}

	kb := [][]tb.InlineButton{{retBtn}}

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (subscription *SubscriptionKeyboard) Main(chat *users.User) (tb.SendOptions, [][]tb.InlineButton) {
	// Has user enabled all notifications?
	allEnabled := false

	if len(chat.UnsubscribedFrom) != 0 {
		// User has unsubscribed from some launches
		allEnabled = false
	} else {
		// User has not unsubscribed from anything: use SubscribedAll's state
		allEnabled = chat.SubscribedAll
	}

	toggleAllBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   map[bool]string{true: "🌍 Tap to disable all 🔕", false: "🌍 Tap to enable all 🔔"}[allEnabled],
		Data:   fmt.Sprintf("all/%s", utils.ToggleBoolStateAsString[allEnabled]),
	}

	// A dynamically generated keyboard array
	kb := [][]tb.InlineButton{{toggleAllBtn}}
	row := []tb.InlineButton{}

	// Generate the keyboard dynamically from available country-codes
	for i, countryCode := range db.CountryCodes {
		row = append(row,
			tb.InlineButton{
				Unique: "countryCodeView",
				Text:   db.CountryCodeToName[countryCode],
				Data:   fmt.Sprintf("cc/%s", countryCode),
			})

		if len(row) == 2 || i == len(db.CountryCodes)-1 {
			kb = append(kb, row)
			row = []tb.InlineButton{}
		}
	}

	// Add the return key
	kb = append(kb, []tb.InlineButton{{
		Unique: "settings",
		Text:   "⬅️ Return",
		Data:   "main",
	}})

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (subscription *SubscriptionKeyboard) ByCountryCode(chat *users.User, cc string) (tb.SendOptions, [][]tb.InlineButton) {
	// Status of all being enabled for this country code
	allEnabled := true

	// A dynamically generated keyboard array
	kb := [][]tb.InlineButton{}
	row := []tb.InlineButton{}

	// Country-code we want to view is at index 1: build the keyboard, and get status for all
	for i, id := range db.IdByCountryCode[cc] {
		enabled := chat.GetNotificationStatusById(id)

		// If not enabled, set allEnabled to false
		if !enabled {
			allEnabled = false
		}

		row = append(row,
			tb.InlineButton{
				Unique: "notificationToggle",
				Text:   fmt.Sprintf("%s %s", utils.BoolStateIndicator[enabled], db.LSPShorthands[id].Name),
				Data:   fmt.Sprintf("id/%d/%s", id, map[bool]string{true: "0", false: "1"}[enabled]),
			})

		if len(row) == 2 || i == len(db.IdByCountryCode[cc])-1 {
			kb = append(kb, row)
			row = []tb.InlineButton{}
		}
	}

	// Add the return key
	kb = append(kb, []tb.InlineButton{{
		Unique: "settings",
		Text:   "⬅️ Return",
		Data:   "sub/bycountry",
	}})

	ccFlag := utils.CountryCodeFlag(cc)

	// Insert the toggle-all key at the beginning
	toggleAllBtn := tb.InlineButton{
		Unique: "notificationToggle",
		Text:   fmt.Sprintf("%s %s", map[bool]string{true: "🔕 Tap to disable all", false: "🔔 Tap to enable all"}[allEnabled], ccFlag),
		Data:   fmt.Sprintf("cc/%s/%s", cc, map[bool]string{true: "0", false: "1"}[allEnabled]),
	}

	// Insert at the beginning of the keyboard
	kb = append([][]tb.InlineButton{{toggleAllBtn}}, kb...)

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (command *CommandKeyboard) Statistics() (tb.SendOptions, [][]tb.InlineButton) {
	// Construct the keyboard and send-options
	kb := [][]tb.InlineButton{{
		tb.InlineButton{
			Unique: "stats",
			Text:   "🔄 Refresh data",
			Data:   "r",
		}},
	}

	sendOptions := tb.SendOptions{
		ParseMode:   "MarkdownV2",
		ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
	}

	return sendOptions, kb
}

func (command *CommandKeyboard) Start() (tb.SendOptions, [][]tb.InlineButton) {
	// Set buttons
	settingsBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "⚙️ Go to LaunchBot settings",
		Data:   "main/newMessage",
	}

	kb := [][]tb.InlineButton{{settingsBtn}}

	sendOptions := tb.SendOptions{
		ParseMode:   "MarkdownV2",
		ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
	}

	return sendOptions, kb
}

func (command *CommandKeyboard) Schedule(mode string) (tb.SendOptions, [][]tb.InlineButton) {
	// Refresh button (refresh/$mode)
	updateBtn := tb.InlineButton{
		Unique: "schedule",
		Text:   "🔄 Refresh",
		Data:   fmt.Sprintf("r/%s", mode),
	}

	// Init the mode-switch button
	modeBtn := tb.InlineButton{Unique: "schedule"}

	switch mode {
	case "v":
		modeBtn.Text = "🛰️ Show missions"
		modeBtn.Data = "m/m"
	case "m":
		modeBtn.Text = "🚀 Show vehicles"
		modeBtn.Data = "m/v"
	default:
		log.Warn().Msgf("Mode defaulted in schedule keyboard generation, mode=%s", mode)
		modeBtn.Text = "🛰️ Show missions"
		modeBtn.Data = "m/m"
	}

	// Construct the keyboard
	kb := [][]tb.InlineButton{{updateBtn, modeBtn}}

	// Send options: new keyboard
	sendOptions := tb.SendOptions{
		ParseMode:   "MarkdownV2",
		ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
	}

	return sendOptions, kb
}

func (command *CommandKeyboard) Next(index int, cacheLength int) (tb.SendOptions, [][]tb.InlineButton) {
	// Create return kb
	var kb [][]tb.InlineButton

	switch index {
	case 0: // Case: first index
		if cacheLength > 1 {
			// Only add the next button if cache is longer than 1
			nextBtn := tb.InlineButton{
				Unique: "next",
				Text:   "Next launch ➡️", Data: "n/1/+",
			}

			kb = [][]tb.InlineButton{{nextBtn}}
		}

		refreshBtn := tb.InlineButton{
			Unique: "next",
			Text:   "Refresh 🔄", Data: "r/0",
		}

		kb = append(kb, []tb.InlineButton{refreshBtn})

	case cacheLength - 1: // Case: last index
		refreshBtn := tb.InlineButton{
			Unique: "next",
			Text:   "Refresh 🔄", Data: fmt.Sprintf("r/%d", index),
		}

		returnBtn := tb.InlineButton{
			Unique: "next",
			Text:   "↩️ Back to first", Data: "n/0/0",
		}

		prevBtn := tb.InlineButton{
			Unique: "next",
			Text:   "⬅️ Previous launch", Data: fmt.Sprintf("n/%d/-", index-1),
		}

		// Construct the keyboard
		kb = [][]tb.InlineButton{{prevBtn}, {returnBtn, refreshBtn}}

	default: // Default case, i.e. not either end of the launch list
		if index > cacheLength-1 {
			// Make sure we don't go over the maximum index
			index = cacheLength - 1
		}

		refreshBtn := tb.InlineButton{
			Unique: "next",
			Text:   "Refresh 🔄", Data: fmt.Sprintf("r/%d", index),
		}

		returnBtn := tb.InlineButton{
			Unique: "next",
			Text:   "↩️ Back to first", Data: "n/0/0",
		}

		nextBtn := tb.InlineButton{
			Unique: "next",
			Text:   "Next ➡️", Data: fmt.Sprintf("n/%d/+", index+1),
		}

		prevBtn := tb.InlineButton{
			Unique: "next",
			Text:   "⬅️ Previous", Data: fmt.Sprintf("n/%d/-", index-1),
		}

		// Construct the keyboard
		kb = [][]tb.InlineButton{{prevBtn, nextBtn}, {returnBtn, refreshBtn}}
	}

	sendOptions := tb.SendOptions{
		ParseMode:   "MarkdownV2",
		ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
	}

	return sendOptions, kb
}

func (command *CommandKeyboard) Expand(id string, notification string, muted bool) (tb.SendOptions, [][]tb.InlineButton) {
	muteBtn := tb.InlineButton{
		Unique: "muteToggle",
		Text:   map[bool]string{true: "🔊 Unmute launch", false: "🔇 Mute launch"}[muted],
		Data:   fmt.Sprintf("%s/%s/%s", id, utils.ToggleBoolStateAsString[muted], notification),
	}

	// Construct the keyboard and send-options
	kb := [][]tb.InlineButton{{muteBtn}}

	sendOptions := tb.SendOptions{
		ParseMode:   "MarkdownV2",
		ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
	}

	return sendOptions, kb
}

func (command *CommandKeyboard) Admin() (tb.SendOptions, [][]tb.InlineButton) {
	// Construct the keyboard and send-options
	kb := [][]tb.InlineButton{{
		tb.InlineButton{
			Unique: "admin",
			Text:   "🔄 Refresh data",
		}},
	}

	sendOptions := tb.SendOptions{
		ParseMode:   "MarkdownV2",
		ReplyMarkup: &tb.ReplyMarkup{InlineKeyboard: kb},
	}

	return sendOptions, kb
}

func (keywords *KeywordsKeyboard) Main(chat *users.User) (tb.SendOptions, [][]tb.InlineButton) {
	allowedBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "✅ Allowed Keywords",
		Data:   "allowed/view",
	}

	blockedBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "🚫 Blocked Keywords",
		Data:   "blocked/view",
	}

	helpBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "❔ How It Works",
		Data:   "help",
	}

	retBtn := tb.InlineButton{
		Unique: "settings",
		Text:   "⬅️ Back to settings",
		Data:   "main",
	}

	kb := [][]tb.InlineButton{{allowedBtn}, {blockedBtn}, {helpBtn}, {retBtn}}

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (keywords *KeywordsKeyboard) ViewBlocked(chat *users.User) (tb.SendOptions, [][]tb.InlineButton) {
	addBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "➕ Add Blocked Keyword",
		Data:   "blocked/add",
	}

	clearBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "🗑️ Clear All Blocked",
		Data:   "blocked/clear",
	}

	retBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "⬅️ Back",
		Data:   "main",
	}

	kb := [][]tb.InlineButton{{addBtn}}

	// Add remove buttons for each keyword
	if chat.BlockedKeywords != "" {
		for _, keyword := range strings.Split(chat.BlockedKeywords, ",") {
			kb = append(kb, []tb.InlineButton{{
				Unique: "keywords",
				Text:   fmt.Sprintf("❌ %s", keyword),
				Data:   fmt.Sprintf("blocked/remove/%s", keyword),
			}})
		}
		kb = append(kb, []tb.InlineButton{clearBtn})
	}

	kb = append(kb, []tb.InlineButton{retBtn})

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}

func (keywords *KeywordsKeyboard) ViewAllowed(chat *users.User) (tb.SendOptions, [][]tb.InlineButton) {
	addBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "➕ Add Allowed Keyword",
		Data:   "allowed/add",
	}

	clearBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "🗑️ Clear All Allowed",
		Data:   "allowed/clear",
	}

	retBtn := tb.InlineButton{
		Unique: "keywords",
		Text:   "⬅️ Back",
		Data:   "main",
	}

	kb := [][]tb.InlineButton{{addBtn}}

	// Add remove buttons for each keyword
	if chat.AllowedKeywords != "" {
		for _, keyword := range strings.Split(chat.AllowedKeywords, ",") {
			kb = append(kb, []tb.InlineButton{{
				Unique: "keywords",
				Text:   fmt.Sprintf("❌ %s", keyword),
				Data:   fmt.Sprintf("allowed/remove/%s", keyword),
			}})
		}
		kb = append(kb, []tb.InlineButton{clearBtn})
	}

	kb = append(kb, []tb.InlineButton{retBtn})

	sendOptions := tb.SendOptions{
		ParseMode:             "MarkdownV2",
		DisableWebPagePreview: true,
		ReplyMarkup:           &tb.ReplyMarkup{InlineKeyboard: kb},
		Protected:             true,
	}

	return sendOptions, kb
}
