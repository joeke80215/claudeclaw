package commands

import (
	"github.com/claudeclaw/claudeclaw/internal/discord"
)

// Discord runs the Discord bot as a standalone process.
func Discord() {
	discord.Standalone()
}
