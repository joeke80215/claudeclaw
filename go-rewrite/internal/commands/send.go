package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/claudeclaw/claudeclaw/internal/config"
	"github.com/claudeclaw/claudeclaw/internal/runner"
	"github.com/claudeclaw/claudeclaw/internal/sessions"
)

// Send runs a prompt against the active daemon session and optionally forwards
// the result to Telegram and/or Discord.
func Send(args []string) {
	telegramFlag := false
	discordFlag := false
	var messageParts []string

	for _, arg := range args {
		switch arg {
		case "--telegram":
			telegramFlag = true
		case "--discord":
			discordFlag = true
		default:
			messageParts = append(messageParts, arg)
		}
	}

	message := strings.TrimSpace(strings.Join(messageParts, " "))
	if message == "" {
		fmt.Fprintln(os.Stderr, "Usage: claudeclaw send <message> [--telegram] [--discord]")
		os.Exit(1)
	}

	if err := config.InitConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Config init error: %v\n", err)
		os.Exit(1)
	}
	if _, err := config.LoadSettings(); err != nil {
		fmt.Fprintf(os.Stderr, "Settings load error: %v\n", err)
		os.Exit(1)
	}

	session, err := sessions.GetSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Session error: %v\n", err)
		os.Exit(1)
	}
	if session == nil {
		fmt.Fprintln(os.Stderr, "No active session. Start the daemon first.")
		os.Exit(1)
	}

	result, err := runner.RunUserMessage(context.Background(), "send", message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Run error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result.Stdout)

	if telegramFlag {
		settings := config.GetSettings()
		token := settings.Telegram.Token
		userIDs := settings.Telegram.AllowedUserIds

		if token == "" || len(userIDs) == 0 {
			fmt.Fprintln(os.Stderr, "Telegram is not configured in settings.")
			os.Exit(1)
		}

		var text string
		if result.ExitCode == 0 {
			text = result.Stdout
			if text == "" {
				text = "(empty)"
			}
		} else {
			stderr := result.Stderr
			if stderr == "" {
				stderr = "Unknown"
			}
			text = fmt.Sprintf("error (exit %d): %s", result.ExitCode, stderr)
		}

		for _, userID := range userIDs {
			body, _ := json.Marshal(map[string]interface{}{
				"chat_id": userID,
				"text":    text,
			})
			url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
			resp, err := http.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to send to Telegram user %d: %v\n", userID, err)
				continue
			}
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				fmt.Fprintf(os.Stderr, "Failed to send to Telegram user %d: %s\n", userID, resp.Status)
			}
		}
		fmt.Println("Sent to Telegram.")
	}

	if discordFlag {
		settings := config.GetSettings()
		dToken := settings.Discord.Token
		dUserIDs := settings.Discord.AllowedUserIds

		if dToken == "" || len(dUserIDs) == 0 {
			fmt.Fprintln(os.Stderr, "Discord is not configured in settings.")
			os.Exit(1)
		}

		var dText string
		if result.ExitCode == 0 {
			dText = result.Stdout
			if dText == "" {
				dText = "(empty)"
			}
		} else {
			stderr := result.Stderr
			if stderr == "" {
				stderr = "Unknown"
			}
			dText = fmt.Sprintf("error (exit %d): %s", result.ExitCode, stderr)
		}

		for _, userID := range dUserIDs {
			// Create DM channel
			dmBody, _ := json.Marshal(map[string]string{"recipient_id": userID})
			dmReq, _ := http.NewRequest("POST", "https://discord.com/api/v10/users/@me/channels", bytes.NewReader(dmBody))
			dmReq.Header.Set("Authorization", "Bot "+dToken)
			dmReq.Header.Set("Content-Type", "application/json")

			dmResp, err := http.DefaultClient.Do(dmReq)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create DM for Discord user %s: %v\n", userID, err)
				continue
			}
			var dmChannel struct {
				ID string `json:"id"`
			}
			json.NewDecoder(dmResp.Body).Decode(&dmChannel)
			dmResp.Body.Close()
			if dmResp.StatusCode >= 400 {
				fmt.Fprintf(os.Stderr, "Failed to create DM for Discord user %s: %s\n", userID, dmResp.Status)
				continue
			}

			// Send message (truncate to 2000 chars)
			msgText := dText
			if len(msgText) > 2000 {
				msgText = msgText[:2000]
			}
			msgBody, _ := json.Marshal(map[string]string{"content": msgText})
			msgURL := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", dmChannel.ID)
			msgReq, _ := http.NewRequest("POST", msgURL, bytes.NewReader(msgBody))
			msgReq.Header.Set("Authorization", "Bot "+dToken)
			msgReq.Header.Set("Content-Type", "application/json")

			msgResp, err := http.DefaultClient.Do(msgReq)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to send to Discord user %s: %v\n", userID, err)
				continue
			}
			msgResp.Body.Close()
			if msgResp.StatusCode >= 400 {
				fmt.Fprintf(os.Stderr, "Failed to send to Discord user %s: %s\n", userID, msgResp.Status)
			}
		}
		fmt.Println("Sent to Discord.")
	}

	if result.ExitCode != 0 {
		os.Exit(result.ExitCode)
	}
}
