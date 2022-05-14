package utils

import (
	"fmt"
	"strings"

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

// Map a stringified binary digit to a boolean state
var BinStringStateToBool = map[string]bool{
	"0": false, "1": true,
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
