package utils

import (
	"fmt"
	"strings"

	emoji "github.com/jayco/go-emoji-flag"
	"github.com/rs/zerolog/log"
	"golang.org/x/exp/slices"
)

// https://ll.thespacedevs.com/2.2.0/config/launchstatus/
var StatusNameToIndicator = map[string]string{
	"Partial Failure": "💥",
	"Failure":         "💥",
	"Success":         "🚀",
	"In Flight":       "🚀",
	"Hold":            "⏸️",
	"Go":              "🟢", // Go, as in a verified launch time
	"TBC":             "🟡", // Unconfirmed launch time
	"TBD":             "🔴", // Unverified launch time
}

// Map a stringified binary digit to the corresponding boolean state
var BinStringStateToBool = map[string]bool{
	"0": false, "1": true,
}

// Map a boolean status to a notification state indicator
var BoolStateIndicator = map[bool]string{
	true: "✅", false: "🔕",
}

// Map a boolean state to "enabled" or "disabled"
var BoolStateString = map[bool]string{
	true: "Enabled", false: "Disabled",
}

// Return a stringified binary-state a bool-state would be toggled to.
// If current state is 'true', the map will return '0'.
// If the current state is 'false', the map will return '1'.
var ToggleBoolStateAsString = map[bool]string{
	true: "0", false: "1",
}

// Map an integer percentage probability into an indicator string
func ProbabilityIndicator(probability int) string {
	var indicator string

	switch {
	case probability == 100:
		indicator = "☀️"
	case probability >= 80:
		indicator = "🌤️"
	case probability >= 60:
		indicator = "🌥️"
	case probability >= 40:
		indicator = "☁️"
	case probability >= 20:
		indicator = "🌧️"
	case probability > 0:
		indicator = "⛈️"
	case probability <= 0:
		indicator = "🌪️"
	}

	return indicator
}

func CountryCodeFlag(cc string) string {
	if cc == "EU" {
		return "🇪🇺"
	}

	return emoji.GetFlag(cc)
}

func NotificationToggleCallbackString(newState bool) string {
	return fmt.Sprintf("%s %s", BoolStateIndicator[newState], BoolStateString[newState])
}

/*
Prepares textual input for Telegram's Markdown parser.

Mode is "text" or "link".

Ref: https://core.telegram.org/bots/api#markdownv2-style
*/
func PrepareInputForMarkdown(input string, mode string) string {
	var escapedChars []rune

	switch mode {
	case "link":
		escapedChars = []rune{')', '\\'}
	case "text":
		escapedChars = []rune{
			'[', ']', '(', ')', '~', '>', '#', '+',
			'-', '=', '|', '{', '}', '.', '!', '_',
		}
	case "italictext":
		escapedChars = []rune{
			'[', ']', '(', ')', '~', '>', '#', '+',
			'-', '=', '|', '{', '}', '.', '!',
		}
	default:
		log.Fatal().Msgf("Invalid mode in PrepareInputForMarkdown (mode=%s)", mode)
	}

	var escapableIdx int
	output := ""

	for _, char := range input {
		// Check if char exists in the list of characters to be escaped
		escapableIdx = slices.Index(escapedChars, char)

		if escapableIdx != -1 {
			// If exists, escape it and add to string
			output += fmt.Sprintf("\\%s", string(escapedChars[escapableIdx]))
		} else {
			output += string(char)
		}
	}

	return output
}

// Converts text to back-ticked, code-formatted text for improved density.
func Monospaced(input string) string {
	output := []string{}

	// Enclose each word in backticks
	for _, word := range strings.Fields(input) {
		output = append(output, fmt.Sprintf("`%s`", word))
	}

	return strings.Join(output, " ")
}
