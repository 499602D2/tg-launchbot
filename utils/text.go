package utils

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	emoji "github.com/jayco/go-emoji-flag"
	"github.com/rs/zerolog/log"
	"golang.org/x/exp/slices"
	tb "gopkg.in/telebot.v3"
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

Mode is "text", "italictext", "link".

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
	case "markdown":
		// Special mode that preserves markdown formatting
		escapedChars = []rune{
			'[', ']', '(', ')', '~', '>', '#', '+',
			'-', '=', '|', '{', '}', '.', '!', '\\',
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

// ReconstructMarkdownFromEntities reconstructs markdown formatting from Telegram message entities
func ReconstructMarkdownFromEntities(text string, entities []tb.MessageEntity) string {
	if len(entities) == 0 {
		return text
	}

	// Convert string to UTF-16 code units to handle offsets correctly
	// Telegram uses UTF-16 offsets, not byte or rune offsets
	textRunes := []rune(text)
	utf16Text := utf16.Encode(textRunes)
	
	// Create a slice to track formatting at each position
	type formatting struct {
		utf16Pos int    // Position in UTF-16 code units
		runePos  int    // Position in runes
		open     bool
		format   string
	}
	
	var formats []formatting
	
	// Process each entity
	for _, entity := range entities {
		var openTag, closeTag string
		
		switch entity.Type {
		case tb.EntityBold:
			openTag, closeTag = "*", "*"
		case tb.EntityItalic:
			openTag, closeTag = "_", "_"
		case tb.EntityCode:
			openTag, closeTag = "`", "`"
		case tb.EntityCodeBlock:
			openTag, closeTag = "```", "```"
		default:
			continue // Skip unsupported entity types
		}
		
		// Convert UTF-16 positions to rune positions
		startRunePos := 0
		endRunePos := 0
		utf16Pos := 0
		
		for i, r := range textRunes {
			if utf16Pos == entity.Offset {
				startRunePos = i
			}
			if utf16Pos == entity.Offset + entity.Length {
				endRunePos = i
				break
			}
			// Count UTF-16 code units (surrogate pairs count as 2)
			if r > 0xFFFF {
				utf16Pos += 2
			} else {
				utf16Pos += 1
			}
		}
		
		// If we didn't find the end position, it's at the end of the string
		if endRunePos == 0 && entity.Offset + entity.Length >= len(utf16Text) {
			endRunePos = len(textRunes)
		}
		
		// Add opening and closing tags
		formats = append(formats, 
			formatting{utf16Pos: entity.Offset, runePos: startRunePos, open: true, format: openTag},
			formatting{utf16Pos: entity.Offset + entity.Length, runePos: endRunePos, open: false, format: closeTag},
		)
	}
	
	// Sort by rune position (reverse order for closing tags at same position)
	sort.Slice(formats, func(i, j int) bool {
		if formats[i].runePos == formats[j].runePos {
			// Closing tags come before opening tags at the same position
			return !formats[i].open && formats[j].open
		}
		return formats[i].runePos < formats[j].runePos
	})
	
	// Build the result string
	var result strings.Builder
	lastPos := 0
	
	for _, f := range formats {
		// Add text up to this position
		if f.runePos > lastPos && f.runePos <= len(textRunes) {
			result.WriteString(string(textRunes[lastPos:f.runePos]))
		}
		// Add the formatting tag
		result.WriteString(f.format)
		lastPos = f.runePos
	}
	
	// Add any remaining text
	if lastPos < len(textRunes) {
		result.WriteString(string(textRunes[lastPos:]))
	}
	
	return result.String()
}
