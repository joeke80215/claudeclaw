package commands

import (
	"github.com/claudeclaw/claudeclaw/internal/telegram"
)

// Telegram runs the Telegram bot as a standalone process.
func Telegram() {
	telegram.Telegram()
}
