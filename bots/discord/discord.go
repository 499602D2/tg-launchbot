package discord

import (
	"launchbot/bots"

	dg "github.com/bwmarrin/discordgo"
)

type Bot struct {
	Bot   *dg.Session
	Queue *bots.Queue
	Owner string
}
