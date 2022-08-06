package templates

// Main template for Telegram
type Telegram struct {
	Keyboard Keyboard
	Messages Messages
}

func (template *Telegram) Init() {
	// Init keyboard templates
	template.Keyboard = Keyboard{
		Settings: SettingsKeyboard{}, Command: CommandKeyboard{},
	}

	// Init message templates
	template.Messages = Messages{
		Settings: SettingsMessage{}, Command: CommandMessage{},
	}
}
