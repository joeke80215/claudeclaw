package main

import (
	"os"

	"github.com/claudeclaw/claudeclaw/internal/commands"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		commands.Start(nil)
		return
	}
	switch args[0] {
	case "--stop-all":
		commands.StopAll()
	case "--stop":
		commands.Stop()
	case "--clear":
		commands.Clear()
	case "start":
		commands.Start(args[1:])
	case "status":
		commands.Status(args[1:])
	case "telegram":
		commands.Telegram()
	case "discord":
		commands.Discord()
	case "send":
		commands.Send(args[1:])
	default:
		commands.Start(nil)
	}
}
