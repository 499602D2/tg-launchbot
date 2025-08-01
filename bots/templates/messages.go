package templates

import (
	"fmt"
	"launchbot/users"
	"launchbot/utils"
	"strings"
)

// Message template wrapper
type Messages struct {
	Settings SettingsMessage
	Command  CommandMessage
	Service  ServiceMessage
}

// Settings sub-sections
type SettingsMessage struct {
	TimeZone     TimeZoneMessage
	Subscription SubscriptionMessage
	Keywords     KeywordsMessage
}

type TimeZoneMessage struct{}
type SubscriptionMessage struct{}
type KeywordsMessage struct{}
type CommandMessage struct{}
type ServiceMessage struct{}

func (messages *Messages) Migrated() string {
	return "🌟 LaunchBot has been upgraded! Please send the command again, instead of " +
		"using the buttons."
}

// Settings.Main
func (settings *SettingsMessage) Main(isGroup bool) string {
	base := "*LaunchBot* | *User settings*\n" +
		"🚀 *Launch subscription settings* allow you to choose what launches you receive notifications for, like SpaceX's or NASA's.\n\n" +
		"🔍 *Keyword filters* let you allow and block launch notifications with arbitrary keywords.\n\n" +
		"⏰ *Notification settings* allow you to choose when you receive notifications.\n\n" +
		"🌍 *Time zone settings* let you set your time zone, so all dates and times are in your local time, instead of UTC+0."

	if isGroup {
		return base + "\n\n👷 *Group settings* let admins change some group-specific settings, such as allowing all users to send commands."
	}

	return base
}

// Settings.Group
func (settings *SettingsMessage) Group() string {
	return "👷 *LaunchBot* | *Group settings*\n" +
		"These are LaunchBot's settings only available to groups, which will be expanded in the future. Currently, " +
		"they allow admins to enable command-access to all group participants."
}

// Settings.Notifications
func (settings *SettingsMessage) Notifications() string {
	return "⏰ *LaunchBot* | *Notification time settings*\n" +
		"Notifications are delivered 24 hours, 12 hours, 60 minutes, and 5 minutes before a launch.\n\n" +
		"By default, you will receive a notification 24 hours before, and 5 minutes before a launch. You can adjust this behavior here.\n\n" +
		"You can also toggle postpone notifications, which are sent when a launch has its launch time moved (if a notification has already been sent)."
}

// Settings.TimeZone.Main
func (tz *TimeZoneMessage) Main(userTz string) string {
	base := "🌍 *LaunchBot* | *Time zone settings*\n" +
		"LaunchBot sets your time zone with the help of Telegram's location sharing feature.\n\n" +
		"This is entirely privacy preserving, as your exact location is not required. Only the general " +
		"location is stored in the form of LINKHERE, such as Europe/Berlin or America/Lima.\n\n" +
		"*Your current time zone is: USERTIMEZONE.* You can remove your time zone information from LaunchBot's server at any time."

	// Set user's time zone, escape markdown
	base = strings.ReplaceAll(base, "USERTIMEZONE", userTz)
	base = utils.PrepareInputForMarkdown(base, "text")

	// Set link
	link := fmt.Sprintf("[a time zone database entry](%s)",
		utils.PrepareInputForMarkdown("https://en.wikipedia.org/wiki/List_of_tz_database_time_zones", "link"))
	base = strings.ReplaceAll(base, "LINKHERE", link)

	return base

}

// Settings.TimeZone.Setup
func (tz *TimeZoneMessage) Setup() string {
	return "🌍 *LaunchBot* | *Time zone set-up*\n" +
		"To complete the time zone setup, follow the instructions below using your phone:\n\n" +
		"*1.* Make sure you are *replying to this message!* *(*`↩️ Reply`*)*\n\n" +
		"*2.* Tap 📎 next to the text field, then choose `📍` `Location`.\n\n" +
		"*3.* As a reply, send the bot a location that is in your time zone. This can be a different city, or even a different country!" +
		"\n\n*Note:* location sharing is not supported in Telegram Desktop, so use your phone or tablet!"
}

// Settings.TimeZone.OnDelete
func (tz *TimeZoneMessage) Deleted(userTz string) string {
	text := fmt.Sprintf(
		"🌍 *LaunchBot* | *Time zone settings*\n"+
			"Your time zone information was successfully deleted! "+
			"Your new time zone is: *%s.*", userTz,
	)

	return text
}

// Settings.Subscription.ByCountryCode
func (subscription *SubscriptionMessage) ByCountryCode() string {
	// TODO add user's time zone
	return "🚀 *LaunchBot* | *Subscription settings*\n" +
		"You can search for specific launch-providers with the country flags, or simply enable notifications for all launch providers.\n\n" +
		"As an example, SpaceX can be found under the 🇺🇸-flag, and ISRO can be found under 🇮🇳-flag. You can also choose to enable all notifications."
}

// Command.Start
func (command *CommandMessage) Start(isGroup bool) string {
	base := "🌟 *Welcome to LaunchBot!* LaunchBot is your one-stop shop into the world of rocket launches. Subscribe to the launches of your favorite " +
		"space agency, or follow that one rocket company you're a fan of.\n\n" +
		"🐙 *LaunchBot is open-source, 100 % free, and respects your privacy.* If you're a developer and want to see a new feature, " +
		"you can open a pull request in GITHUBLINK\n\n" +
		"🌠 *To get started, you can subscribe to some notifications, or try out the commands.* If you have any feedback, or a request for improvement, " +
		"you can use the feedback command."

	group := "\n\n👷 *Note for group admins!* To reduce spam, LaunchBot only responds to requests by admins. " +
		"LaunchBot can also automatically delete commands it won't reply to, if given the permission to delete messages. " +
		"If you'd like everyone to be able to send commands, just flip a switch in the settings!"

	if isGroup {
		return base + group
	}

	return base
}

// Command.Feedback
func (command *CommandMessage) Feedback(received bool) string {
	if received {
		return "🌟 *Thank you for your feedback!* Your feedback was received successfully."
	}

	return "🌟 *LaunchBot* | *Developer feedback*\n" +
		"Here, you can send feedback that goes directly to the developer. To send feedback, just write a message that starts with /feedback!\n\n" +
		"An example would be `/feedback Great bot, thank you!`\n\n" +
		"*Thank you for using LaunchBot! <3*"
}

func (service *ServiceMessage) InteractionNotAllowed() string {
	return "🙃 Whoops, you must be an admin of this group to do that!"
}

// Keywords.Main
func (keywords *KeywordsMessage) Main(chat *users.User) string {
	return "🔍 *LaunchBot* | *Keyword Filtering*\n" +
		"Control which launches you receive notifications for using keywords.\n\n" +
		"*Allowed keywords:* Launches matching these keywords will be included, regardless of your subscriptions.\n" +
		"*Blocked keywords:* Launches matching these keywords will be excluded, regardless of your subscriptions.\n\n" +
		"Use the buttons below to manage your keywords."
}

// Keywords.ViewBlocked
func (keywords *KeywordsMessage) ViewBlocked(chat *users.User) string {
	base := "🚫 *LaunchBot* | *Blocked Keywords*\n"

	if chat.BlockedKeywords == "" {
		return base + "You haven't blocked any keywords yet.\n\n" +
			"Add keywords to exclude launches containing those words. For example, blocking \"Starlink\" will stop notifications for all Starlink launches."
	}

	blockedList := strings.Split(chat.BlockedKeywords, ",")
	return base + fmt.Sprintf("You have blocked %d keyword(s). Tap on a keyword to remove it, or add new ones.", len(blockedList))
}

// Keywords.ViewAllowed
func (keywords *KeywordsMessage) ViewAllowed(chat *users.User) string {
	base := "✅ *LaunchBot* | *Allowed Keywords*\n"

	if chat.AllowedKeywords == "" {
		return base + "You haven't allowed any keywords yet.\n\n" +
			"Add keywords to receive notifications for launches containing those words, regardless of your subscriptions. For example, allowing \"Falcon\" will notify you of all Falcon rocket launches."
	}

	allowedList := strings.Split(chat.AllowedKeywords, ",")
	return base + fmt.Sprintf("You have allowed %d keyword(s). Tap on a keyword to remove it, or add new ones.", len(allowedList))
}

// Keywords.Help
func (keywords *KeywordsMessage) Help() string {
	return "❔ *LaunchBot* | *Keyword Filtering Help*\n\n" +
		"*How it works:*\n" +
		"• *Blocked keywords:* Always exclude matching launches\n" +
		"• *Allowed keywords:* Always include matching launches\n" +
		"• Keywords override your launch provider subscriptions\n\n" +
		"*Examples:*\n" +
		"• Block \"Starlink\" to skip all Starlink launches\n" +
		"• Allow \"Starship\" to get all Starship launches\n\n" +
		"*Tips:*\n" +
		"• Keywords are case-insensitive\n" +
		"• Partial matches work (\"Star\" matches \"Starship\" and \"Starlink\")\n" +
		"• Keywords can match launch name or vehicle name\n" +
		"• You can add multiple keywords at once by separating them with commas"
}

// Keywords.AddPrompt
func (keywords *KeywordsMessage) AddPrompt(keywordType string) string {
	action := map[string]string{
		"blocked": "block",
		"allowed": "allow",
	}[keywordType]

	return fmt.Sprintf("Please send the keyword(s) you want to %s.\n\n"+
		"Examples:\n"+
		"• Single: `Starlink`\n"+
		"• Multiple: `Starlink, OneWeb, Kuiper`\n\n"+
		"Send /cancel to cancel.", action)
}

// Keywords.Added
func (keywords *KeywordsMessage) Added(keyword, keywordType string) string {
	action := map[string]string{
		"blocked": "blocked",
		"allowed": "allowed",
	}[keywordType]

	return fmt.Sprintf("✅ Successfully %s keyword: *%s*", action, utils.PrepareInputForMarkdown(keyword, "text"))
}

// Keywords.Removed
func (keywords *KeywordsMessage) Removed(keyword, keywordType string) string {
	action := map[string]string{
		"blocked": "unblocked",
		"allowed": "disallowed",
	}[keywordType]

	return fmt.Sprintf("✅ Successfully %s keyword: *%s*", action, utils.PrepareInputForMarkdown(keyword, "text"))
}

// Keywords.AlreadyExists
func (keywords *KeywordsMessage) AlreadyExists(keyword, keywordType string) string {
	action := map[string]string{
		"blocked": "already blocked",
		"allowed": "already allowed",
	}[keywordType]

	return fmt.Sprintf("⚠️ Keyword *%s* is %s.", utils.PrepareInputForMarkdown(keyword, "text"), action)
}

// Keywords.NotFound
func (keywords *KeywordsMessage) NotFound(keyword, keywordType string) string {
	action := map[string]string{
		"blocked": "blocked",
		"allowed": "allowed",
	}[keywordType]

	return fmt.Sprintf("⚠️ Keyword *%s* is not %s.", utils.PrepareInputForMarkdown(keyword, "text"), action)
}

// Keywords.Cleared
func (keywords *KeywordsMessage) Cleared(keywordType string) string {
	return fmt.Sprintf("✅ All %s keywords have been cleared.", keywordType)
}
