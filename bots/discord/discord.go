package discord

import (
	dg "github.com/bwmarrin/discordgo"
)

type Bot struct {
	Bot   *dg.Session
	Owner string
}
