// Package discord implements the Discord bot using the Gateway WebSocket API
// and REST API. It handles message processing, slash commands, button interactions,
// and voice/image attachments.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"nhooyr.io/websocket"

	"github.com/claudeclaw/claudeclaw/internal/config"
	"github.com/claudeclaw/claudeclaw/internal/runner"
	"github.com/claudeclaw/claudeclaw/internal/sessions"
	"github.com/claudeclaw/claudeclaw/internal/skills"
	"github.com/claudeclaw/claudeclaw/internal/whisper"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	discordAPIBase = "https://discord.com/api/v10"
	defaultGateway = "wss://gateway.discord.gg/?v=10&encoding=json"

	maxMessageLen = 2000

	// Gateway opcodes.
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11

	// Intents bitfield.
	intentGuilds                = 1 << 0
	intentGuildMessages         = 1 << 9
	intentGuildMessageReactions = 1 << 10
	intentDirectMessages        = 1 << 12
	intentMessageContent        = 1 << 15

	intents = intentGuilds | intentGuildMessages | intentGuildMessageReactions |
		intentDirectMessages | intentMessageContent // 33281

	// Interaction types.
	interactionTypeCommand   = 2
	interactionTypeComponent = 3

	// Discord typing indicator lasts ~10s; re-fire every 8s.
	typingTickInterval = 8 * time.Second
)

// Fatal close codes that must not trigger reconnection.
var fatalCloseCodes = map[int]bool{
	4004: true, // Authentication failed
	4010: true, // Invalid shard
	4011: true, // Sharding required
	4012: true, // Invalid API version
	4013: true, // Invalid intent(s)
	4014: true, // Disallowed intent(s)
}

// ---------------------------------------------------------------------------
// Discord data types
// ---------------------------------------------------------------------------

type discordUser struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
	Bot           bool   `json:"bot"`
}

type discordAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
	ProxyURL    string `json:"proxy_url"`
	Size        int    `json:"size"`
	Flags       int    `json:"flags"`
}

type discordMessage struct {
	ID                string              `json:"id"`
	ChannelID         string              `json:"channel_id"`
	GuildID           string              `json:"guild_id"`
	Author            discordUser         `json:"author"`
	Content           string              `json:"content"`
	Attachments       []discordAttachment `json:"attachments"`
	Mentions          []discordUser       `json:"mentions"`
	ReferencedMessage *discordMessage     `json:"referenced_message"`
	Flags             int                 `json:"flags"`
	Type              int                 `json:"type"`
}

type discordInteraction struct {
	ID        string             `json:"id"`
	Type      int                `json:"type"`
	Data      *interactionData   `json:"data"`
	ChannelID string             `json:"channel_id"`
	GuildID   string             `json:"guild_id"`
	Member    *interactionMember `json:"member"`
	User      *discordUser       `json:"user"`
	Token     string             `json:"token"`
	Message   *discordMessage    `json:"message"`
}

type interactionData struct {
	Name     string `json:"name"`
	CustomID string `json:"custom_id"`
}

type interactionMember struct {
	User discordUser `json:"user"`
}

type discordGuild struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	SystemChannelID string `json:"system_channel_id"`
	JoinedAt        string `json:"joined_at"`
}

type gatewayPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int            `json:"s"`
	T  string          `json:"t"`
}

type helloData struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type readyData struct {
	SessionID        string      `json:"session_id"`
	ResumeGatewayURL string      `json:"resume_gateway_url"`
	User             discordUser `json:"user"`
	Application      struct {
		ID string `json:"id"`
	} `json:"application"`
	Guilds []struct {
		ID string `json:"id"`
	} `json:"guilds"`
}

type dmChannel struct {
	ID string `json:"id"`
}

// ---------------------------------------------------------------------------
// Package-level debug state
// ---------------------------------------------------------------------------

var debugEnabled bool

func debugLog(format string, args ...interface{}) {
	if !debugEnabled {
		return
	}
	log.Printf("[Discord][debug] "+format, args...)
}

// ---------------------------------------------------------------------------
// REST API
// ---------------------------------------------------------------------------

// discordAPI calls the Discord REST API, handling 429 rate limits with retry.
func discordAPI(token, method, endpoint string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("discord api: marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, discordAPIBase+endpoint, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("discord api: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("discord api: %s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Rate limit handling.
	if resp.StatusCode == http.StatusTooManyRequests {
		var rl struct {
			RetryAfter float64 `json:"retry_after"`
		}
		if err := json.Unmarshal(respBody, &rl); err == nil && rl.RetryAfter > 0 {
			delay := time.Duration(rl.RetryAfter*1000) * time.Millisecond
			debugLog("Rate limited on %s %s, retrying in %v", method, endpoint, delay)
			time.Sleep(delay)
			return discordAPI(token, method, endpoint, body)
		}
	}

	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, fmt.Errorf("discord api %s %s: %d %s %s",
			method, endpoint, resp.StatusCode, resp.Status, string(respBody))
	}

	// 204 No Content (reactions, typing, etc.).
	if resp.StatusCode == http.StatusNoContent {
		return nil, resp.StatusCode, nil
	}

	return respBody, resp.StatusCode, nil
}

// ---------------------------------------------------------------------------
// Message sending (exported for heartbeat forwarding)
// ---------------------------------------------------------------------------

// SendMessage sends a message to a channel, splitting at 2000 characters.
// Components are attached only to the last chunk.
func SendMessage(token, channelID, text string, components []interface{}) error {
	normalized := reactStripRe.ReplaceAllString(text, "")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return nil
	}

	chunks := splitMessage(normalized, maxMessageLen)
	for i, chunk := range chunks {
		body := map[string]interface{}{"content": chunk}
		if components != nil && i == len(chunks)-1 {
			body["components"] = components
		}
		if _, _, err := discordAPI(token, "POST", "/channels/"+channelID+"/messages", body); err != nil {
			return err
		}
	}
	return nil
}

// SendMessageToUser sends a DM to a user by first creating a DM channel.
func SendMessageToUser(token, userID, text string) error {
	data, _, err := discordAPI(token, "POST", "/users/@me/channels", map[string]string{
		"recipient_id": userID,
	})
	if err != nil {
		return fmt.Errorf("discord: create DM channel for user %s: %w", userID, err)
	}
	var ch dmChannel
	if err := json.Unmarshal(data, &ch); err != nil {
		return fmt.Errorf("discord: parse DM channel: %w", err)
	}
	return SendMessage(token, ch.ID, text, nil)
}

func sendTyping(token, channelID string) {
	_, _, _ = discordAPI(token, "POST", "/channels/"+channelID+"/typing", nil)
}

func sendReaction(token, channelID, messageID, emoji string) {
	encoded := url.PathEscape(emoji)
	endpoint := fmt.Sprintf("/channels/%s/messages/%s/reactions/%s/@me", channelID, messageID, encoded)
	req, err := http.NewRequest("PUT", discordAPIBase+endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		end := maxLen
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	return chunks
}

// ---------------------------------------------------------------------------
// Reaction directive extraction
// ---------------------------------------------------------------------------

var (
	reactStripRe   = regexp.MustCompile(`(?i)\[react:[^\]\r\n]+\]`)
	reactCaptureRe = regexp.MustCompile(`(?i)\[react:([^\]\r\n]+)\]`)
	trailingSpaceRe = regexp.MustCompile(`[ \t]+\n`)
	multiNewlineRe  = regexp.MustCompile(`\n{3,}`)
)

func extractReactionDirective(text string) (cleanedText string, reactionEmoji string) {
	matches := reactCaptureRe.FindAllStringSubmatch(text, -1)
	if len(matches) > 0 {
		reactionEmoji = strings.TrimSpace(matches[0][1])
	}
	cleanedText = reactStripRe.ReplaceAllString(text, "")
	cleanedText = trailingSpaceRe.ReplaceAllString(cleanedText, "\n")
	cleanedText = multiNewlineRe.ReplaceAllString(cleanedText, "\n\n")
	cleanedText = strings.TrimSpace(cleanedText)
	return
}

// ---------------------------------------------------------------------------
// Slash command registration
// ---------------------------------------------------------------------------

func registerSlashCommands(token, applicationID string) {
	commands := []map[string]interface{}{
		{"name": "start", "description": "Show welcome message and usage instructions", "type": 1},
		{"name": "reset", "description": "Reset the global session for a fresh start", "type": 1},
	}
	endpoint := fmt.Sprintf("/applications/%s/commands", applicationID)
	if _, _, err := discordAPI(token, "PUT", endpoint, commands); err != nil {
		log.Printf("[Discord] Failed to register slash commands: %v", err)
		return
	}
	debugLog("Slash commands registered")
}

// ---------------------------------------------------------------------------
// Interaction response
// ---------------------------------------------------------------------------

func respondToInteraction(interactionID, interactionToken string, data map[string]interface{}) {
	body := map[string]interface{}{
		"type": 4, // CHANNEL_MESSAGE_WITH_SOURCE
		"data": data,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return
	}
	endpoint := fmt.Sprintf("%s/interactions/%s/%s/callback", discordAPIBase, interactionID, interactionToken)
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Attachment handling
// ---------------------------------------------------------------------------

func isImageAttachment(a discordAttachment) bool {
	return strings.HasPrefix(a.ContentType, "image/")
}

func isVoiceAttachment(a discordAttachment) bool {
	// IS_VOICE_MESSAGE flag (1 << 13).
	if a.Flags&(1<<13) != 0 {
		return true
	}
	return strings.HasPrefix(a.ContentType, "audio/")
}

func downloadAttachment(attachment discordAttachment, kind string) (string, error) {
	dir := filepath.Join(config.BaseDir(), "inbox", "discord")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("discord: mkdir inbox: %w", err)
	}

	resp, err := http.Get(attachment.URL)
	if err != nil {
		return "", fmt.Errorf("discord: download attachment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord: attachment download failed: %d", resp.StatusCode)
	}

	ext := filepath.Ext(attachment.Filename)
	if ext == "" {
		if kind == "voice" {
			ext = ".ogg"
		} else {
			ext = ".jpg"
		}
	}
	filename := fmt.Sprintf("%s-%d%s", attachment.ID, time.Now().UnixMilli(), ext)
	localPath := filepath.Join(dir, filename)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("discord: read attachment body: %w", err)
	}
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return "", fmt.Errorf("discord: write attachment: %w", err)
	}

	debugLog("Attachment downloaded: %s (%d bytes)", localPath, len(data))
	return localPath, nil
}

// ---------------------------------------------------------------------------
// Gateway struct
// ---------------------------------------------------------------------------

// Gateway manages the Discord WebSocket gateway connection.
type Gateway struct {
	mu sync.Mutex

	conn              *websocket.Conn
	heartbeatInterval time.Duration
	lastSequence      *int
	sessionID         string
	resumeURL         string
	botUserID         string
	botUsername        string
	applicationID     string
	readyGuildIDs     map[string]struct{}
	heartbeatAcked    bool
	debug             bool

	cancel context.CancelFunc
}

// NewGateway creates a new Gateway instance.
func NewGateway() *Gateway {
	return &Gateway{
		readyGuildIDs:  make(map[string]struct{}),
		heartbeatAcked: true,
	}
}

func (g *Gateway) resetState() {
	g.heartbeatInterval = 0
	g.heartbeatAcked = true
	g.lastSequence = nil
	g.sessionID = ""
	g.resumeURL = ""
	g.botUserID = ""
	g.botUsername = ""
	g.applicationID = ""
	g.readyGuildIDs = make(map[string]struct{})
}

// ---------------------------------------------------------------------------
// Guild trigger logic
// ---------------------------------------------------------------------------

func (g *Gateway) guildTriggerReason(msg *discordMessage) string {
	g.mu.Lock()
	bid := g.botUserID
	g.mu.Unlock()

	// Reply to bot.
	if bid != "" && msg.ReferencedMessage != nil && msg.ReferencedMessage.Author.ID == bid {
		return "reply_to_bot"
	}

	// Mention via mentions array.
	if bid != "" {
		for _, m := range msg.Mentions {
			if m.ID == bid {
				return "mention"
			}
		}
	}

	// Mention in content (fallback).
	if bid != "" && strings.Contains(msg.Content, "<@"+bid+">") {
		return "mention_in_content"
	}

	// Listen channel.
	cfg := config.GetSettings().Discord
	for _, ch := range cfg.ListenChannels {
		if ch == msg.ChannelID {
			return "listen_channel"
		}
	}

	return ""
}

// ---------------------------------------------------------------------------
// Message handler
// ---------------------------------------------------------------------------

func (g *Gateway) handleMessageCreate(ctx context.Context, token string, msg *discordMessage) {
	cfg := config.GetSettings().Discord

	// Ignore bot messages.
	if msg.Author.Bot {
		return
	}

	userID := msg.Author.ID
	channelID := msg.ChannelID
	isDM := msg.GuildID == ""
	isGuild := msg.GuildID != ""

	// Guild trigger check.
	triggerReason := "direct_message"
	if isGuild {
		triggerReason = g.guildTriggerReason(msg)
		if triggerReason == "" {
			debugLog("Skip guild message channel=%s from=%s reason=no_trigger", channelID, userID)
			return
		}
	}
	debugLog("Handle message channel=%s from=%s reason=%s text=\"%.80s\"",
		channelID, userID, triggerReason, msg.Content)

	// Authorization check.
	if len(cfg.AllowedUserIds) > 0 && !containsStr(cfg.AllowedUserIds, userID) {
		if isDM {
			_ = SendMessage(cfg.Token, channelID, "Unauthorized.", nil)
		} else {
			debugLog("Skip guild message channel=%s from=%s reason=unauthorized_user", channelID, userID)
		}
		return
	}

	// Detect attachments.
	var imageAttachments, voiceAttachments []discordAttachment
	for _, a := range msg.Attachments {
		if isImageAttachment(a) {
			imageAttachments = append(imageAttachments, a)
		}
		if isVoiceAttachment(a) {
			voiceAttachments = append(voiceAttachments, a)
		}
	}
	hasImage := len(imageAttachments) > 0
	hasVoice := len(voiceAttachments) > 0

	if strings.TrimSpace(msg.Content) == "" && !hasImage && !hasVoice {
		return
	}

	// Strip bot mention from content.
	cleanContent := msg.Content
	g.mu.Lock()
	bid := g.botUserID
	g.mu.Unlock()
	if bid != "" {
		mentionRe := regexp.MustCompile(`<@!?` + regexp.QuoteMeta(bid) + `>`)
		cleanContent = strings.TrimSpace(mentionRe.ReplaceAllString(cleanContent, ""))
	}

	label := msg.Author.Username
	var mediaParts []string
	if hasImage {
		mediaParts = append(mediaParts, "image")
	}
	if hasVoice {
		mediaParts = append(mediaParts, "voice")
	}
	mediaSuffix := ""
	if len(mediaParts) > 0 {
		mediaSuffix = " [" + strings.Join(mediaParts, "+") + "]"
	}
	truncated := cleanContent
	if len(truncated) > 60 {
		truncated = truncated[:60] + "..."
	}
	log.Printf("[%s] Discord %s%s: \"%s\"",
		time.Now().Format("15:04:05"), label, mediaSuffix, truncated)

	// Typing indicator loop: send typing every 8s until context is cancelled.
	typingCtx, typingCancel := context.WithCancel(ctx)
	defer typingCancel()
	go func() {
		sendTyping(cfg.Token, channelID)
		ticker := time.NewTicker(typingTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				sendTyping(cfg.Token, channelID)
			}
		}
	}()

	var imagePath, voicePath, voiceTranscript string

	if hasImage {
		p, err := downloadAttachment(imageAttachments[0], "image")
		if err != nil {
			log.Printf("[Discord] Failed to download image for %s: %v", label, err)
		} else {
			imagePath = p
		}
	}

	if hasVoice {
		p, err := downloadAttachment(voiceAttachments[0], "voice")
		if err != nil {
			log.Printf("[Discord] Failed to download voice for %s: %v", label, err)
		} else {
			voicePath = p
		}
		if voicePath != "" {
			debugLog("Voice file saved: path=%s", voicePath)
			transcript, err := whisper.TranscribeAudioToText(voicePath, g.debug)
			if err != nil {
				log.Printf("[Discord] Failed to transcribe voice for %s: %v", label, err)
			} else {
				voiceTranscript = transcript
			}
		}
	}

	// Skill routing: detect slash commands and resolve to SKILL.md prompts.
	var command, skillContext string
	if strings.HasPrefix(cleanContent, "/") {
		parts := strings.Fields(strings.TrimSpace(cleanContent))
		if len(parts) > 0 {
			command = strings.ToLower(parts[0])
		}
	}
	if command != "" {
		sc, err := skills.ResolveSkillPrompt(command)
		if err != nil {
			debugLog("Skill resolution failed for %s: %v", command, err)
		} else if sc != "" {
			skillContext = sc
			debugLog("Skill resolved for %s: %d chars", command, len(skillContext))
		}
	}

	// Build prompt.
	promptParts := []string{fmt.Sprintf("[Discord from %s]", label)}

	if skillContext != "" {
		args := strings.TrimSpace(cleanContent)
		if len(args) > len(command) {
			args = strings.TrimSpace(args[len(command):])
		} else {
			args = ""
		}
		promptParts = append(promptParts, fmt.Sprintf("<command-name>%s</command-name>", command))
		promptParts = append(promptParts, skillContext)
		if args != "" {
			promptParts = append(promptParts, "User arguments: "+args)
		}
	} else if strings.TrimSpace(cleanContent) != "" {
		promptParts = append(promptParts, "Message: "+cleanContent)
	}

	if imagePath != "" {
		promptParts = append(promptParts, "Image path: "+imagePath)
		promptParts = append(promptParts, "The user attached an image. Inspect this image file directly before answering.")
	} else if hasImage {
		promptParts = append(promptParts, "The user attached an image, but downloading it failed. Respond and ask them to resend.")
	}

	if voiceTranscript != "" {
		promptParts = append(promptParts, "Voice transcript: "+voiceTranscript)
		promptParts = append(promptParts, "The user attached voice audio. Use the transcript as their spoken message.")
	} else if hasVoice {
		promptParts = append(promptParts, "The user attached voice audio, but it could not be transcribed. Respond and ask them to resend a clearer clip.")
	}

	prefixedPrompt := strings.Join(promptParts, "\n")
	result, err := runner.RunUserMessage("discord", prefixedPrompt)

	// Cancel typing before sending response.
	typingCancel()

	if err != nil {
		log.Printf("[Discord] Error for %s: %v", label, err)
		_ = SendMessage(cfg.Token, channelID, fmt.Sprintf("Error: %v", err), nil)
		return
	}

	if result.ExitCode != 0 {
		errText := result.Stderr
		if errText == "" {
			errText = "Unknown error"
		}
		_ = SendMessage(cfg.Token, channelID,
			fmt.Sprintf("Error (exit %d): %s", result.ExitCode, errText), nil)
	} else {
		cleaned, emoji := extractReactionDirective(result.Stdout)
		if emoji != "" {
			sendReaction(cfg.Token, channelID, msg.ID, emoji)
		}
		if cleaned == "" {
			cleaned = "(empty response)"
		}
		_ = SendMessage(cfg.Token, channelID, cleaned, nil)
	}
}

// ---------------------------------------------------------------------------
// Interaction handler (slash commands + secretary buttons)
// ---------------------------------------------------------------------------

var secButtonRe = regexp.MustCompile(`^sec_(yes|no)_([0-9a-f]{8})$`)

func (g *Gateway) handleInteractionCreate(token string, interaction *discordInteraction) {
	cfg := config.GetSettings().Discord

	actorID := ""
	if interaction.Member != nil {
		actorID = interaction.Member.User.ID
	} else if interaction.User != nil {
		actorID = interaction.User.ID
	}

	if len(cfg.AllowedUserIds) > 0 && (actorID == "" || !containsStr(cfg.AllowedUserIds, actorID)) {
		respondToInteraction(interaction.ID, interaction.Token, map[string]interface{}{
			"content": "Unauthorized.",
			"flags":   64, // EPHEMERAL
		})
		return
	}

	// Slash commands (type 2: APPLICATION_COMMAND).
	if interaction.Type == interactionTypeCommand && interaction.Data != nil {
		switch interaction.Data.Name {
		case "start":
			respondToInteraction(interaction.ID, interaction.Token, map[string]interface{}{
				"content": "Hello! Send me a message and I'll respond using Claude.\nUse `/reset` to start a fresh session.",
			})
			return
		case "reset":
			if err := sessions.ResetSession(); err != nil {
				log.Printf("[Discord] Failed to reset session: %v", err)
			}
			respondToInteraction(interaction.ID, interaction.Token, map[string]interface{}{
				"content": "Global session reset. Next message starts fresh.",
			})
			return
		default:
			respondToInteraction(interaction.ID, interaction.Token, map[string]interface{}{
				"content": "Unknown command.",
			})
			return
		}
	}

	// Button interactions (type 3: MESSAGE_COMPONENT) — secretary workflow.
	if interaction.Type == interactionTypeComponent && interaction.Data != nil {
		customID := interaction.Data.CustomID

		if secMatch := secButtonRe.FindStringSubmatch(customID); secMatch != nil {
			action := secMatch[1]
			pendingID := secMatch[2]
			responseText := "Server error"

			endpoint := fmt.Sprintf("http://127.0.0.1:9999/confirm/%s/%s", pendingID, action)
			resp, err := http.Get(endpoint)
			if err == nil {
				defer resp.Body.Close()
				var result struct {
					OK bool `json:"ok"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
					switch {
					case action == "yes" && result.OK:
						responseText = "Sent!"
					case result.OK:
						responseText = "Dismissed"
					default:
						responseText = "Not found"
					}
				}
			}

			respondToInteraction(interaction.ID, interaction.Token, map[string]interface{}{
				"content": responseText,
				"flags":   64,
			})
			return
		}

		// Default button ack.
		respondToInteraction(interaction.ID, interaction.Token, map[string]interface{}{
			"content": "OK",
			"flags":   64,
		})
		return
	}

	// Default ack for any other interaction type.
	respondToInteraction(interaction.ID, interaction.Token, map[string]interface{}{
		"content": "OK",
		"flags":   64,
	})
}

// ---------------------------------------------------------------------------
// Guild join handler
// ---------------------------------------------------------------------------

func (g *Gateway) handleGuildCreate(token string, guild *discordGuild) {
	cfg := config.GetSettings().Discord

	g.mu.Lock()
	_, alreadyKnown := g.readyGuildIDs[guild.ID]
	g.mu.Unlock()
	if alreadyKnown {
		return
	}

	channelID := guild.SystemChannelID
	if channelID == "" {
		return
	}

	log.Printf("[Discord] Joined guild: %s (%s)", guild.Name, guild.ID)

	eventPrompt := fmt.Sprintf(
		"[Discord system event] I was added to a guild.\nGuild name: %s\nGuild id: %s\n"+
			"Write a short first message for the server. Confirm I was added and explain how to trigger me (mention or reply).",
		guild.Name, guild.ID,
	)

	result, err := runner.Run("discord", eventPrompt)
	if err != nil || result.ExitCode != 0 {
		_ = SendMessage(cfg.Token, channelID, "I was added to this server. Mention me to start.", nil)
		return
	}
	text := result.Stdout
	if text == "" {
		text = "I was added to this server."
	}
	_ = SendMessage(cfg.Token, channelID, text, nil)
}

// ---------------------------------------------------------------------------
// WebSocket helpers
// ---------------------------------------------------------------------------

func (g *Gateway) sendJSON(ctx context.Context, v interface{}) error {
	g.mu.Lock()
	conn := g.conn
	g.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("discord: not connected")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func (g *Gateway) sendHeartbeat(ctx context.Context) {
	g.mu.Lock()
	seq := g.lastSequence
	g.heartbeatAcked = false
	g.mu.Unlock()

	payload := map[string]interface{}{
		"op": opHeartbeat,
		"d":  seq,
	}
	if err := g.sendJSON(ctx, payload); err != nil {
		debugLog("Failed to send heartbeat: %v", err)
	}
}

func (g *Gateway) sendIdentify(ctx context.Context, token string) error {
	return g.sendJSON(ctx, map[string]interface{}{
		"op": opIdentify,
		"d": map[string]interface{}{
			"token":   token,
			"intents": intents,
			"properties": map[string]string{
				"os":      "linux",
				"browser": "claudeclaw",
				"device":  "claudeclaw",
			},
		},
	})
}

func (g *Gateway) sendGatewayResume(ctx context.Context, token string) error {
	g.mu.Lock()
	sid := g.sessionID
	seq := g.lastSequence
	g.mu.Unlock()

	return g.sendJSON(ctx, map[string]interface{}{
		"op": opResume,
		"d": map[string]interface{}{
			"token":      token,
			"session_id": sid,
			"seq":        seq,
		},
	})
}

// ---------------------------------------------------------------------------
// Heartbeat loop
// ---------------------------------------------------------------------------

func (g *Gateway) runHeartbeat(ctx context.Context) {
	g.mu.Lock()
	interval := g.heartbeatInterval
	g.mu.Unlock()
	if interval == 0 {
		return
	}

	// First heartbeat with jitter per Discord spec.
	jitter := time.Duration(rand.Float64() * float64(interval))
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
		g.sendHeartbeat(ctx)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.mu.Lock()
			acked := g.heartbeatAcked
			g.mu.Unlock()
			if !acked {
				debugLog("Heartbeat not acked, reconnecting")
				g.mu.Lock()
				conn := g.conn
				g.mu.Unlock()
				if conn != nil {
					conn.Close(websocket.StatusNormalClosure, "Heartbeat timeout")
				}
				return
			}
			g.sendHeartbeat(ctx)
		}
	}
}

// ---------------------------------------------------------------------------
// Payload / dispatch handlers
// ---------------------------------------------------------------------------

func (g *Gateway) handlePayload(ctx context.Context, token string, payload *gatewayPayload) {
	if payload.S != nil {
		g.mu.Lock()
		seq := *payload.S
		g.lastSequence = &seq
		g.mu.Unlock()
	}

	switch payload.Op {
	case opHello:
		var hello helloData
		if err := json.Unmarshal(payload.D, &hello); err != nil {
			log.Printf("[Discord] Failed to parse HELLO: %v", err)
			return
		}
		g.mu.Lock()
		g.heartbeatInterval = time.Duration(hello.HeartbeatInterval) * time.Millisecond
		canResume := g.sessionID != "" && g.lastSequence != nil
		g.mu.Unlock()

		// Start heartbeat in background.
		go g.runHeartbeat(ctx)

		if canResume {
			if err := g.sendGatewayResume(ctx, token); err != nil {
				log.Printf("[Discord] Failed to send RESUME: %v", err)
			}
		} else {
			if err := g.sendIdentify(ctx, token); err != nil {
				log.Printf("[Discord] Failed to send IDENTIFY: %v", err)
			}
		}

	case opHeartbeatAck:
		g.mu.Lock()
		g.heartbeatAcked = true
		g.mu.Unlock()

	case opHeartbeat:
		// Server-requested heartbeat.
		g.sendHeartbeat(ctx)

	case opReconnect:
		debugLog("Gateway requested reconnect")
		g.mu.Lock()
		conn := g.conn
		g.mu.Unlock()
		if conn != nil {
			conn.Close(websocket.StatusNormalClosure, "Reconnect requested")
		}

	case opInvalidSession:
		var resumable bool
		_ = json.Unmarshal(payload.D, &resumable)
		debugLog("Invalid session, resumable=%v", resumable)

		if !resumable {
			g.mu.Lock()
			g.sessionID = ""
			g.lastSequence = nil
			g.mu.Unlock()
		}

		// Wait 1-5s before re-identifying (per Discord spec).
		delay := time.Duration(1000+rand.IntN(4000)) * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		g.mu.Lock()
		sid := g.sessionID
		g.mu.Unlock()
		if resumable && sid != "" {
			_ = g.sendGatewayResume(ctx, token)
		} else {
			_ = g.sendIdentify(ctx, token)
		}

	case opDispatch:
		g.handleDispatch(ctx, token, payload.T, payload.D)
	}
}

func (g *Gateway) handleDispatch(ctx context.Context, token, eventName string, raw json.RawMessage) {
	debugLog("Dispatch: %s", eventName)

	switch eventName {
	case "READY":
		var ready readyData
		if err := json.Unmarshal(raw, &ready); err != nil {
			log.Printf("[Discord] Failed to parse READY: %v", err)
			return
		}
		g.mu.Lock()
		g.sessionID = ready.SessionID
		g.resumeURL = ready.ResumeGatewayURL
		g.botUserID = ready.User.ID
		g.botUsername = ready.User.Username
		g.applicationID = ready.Application.ID
		g.readyGuildIDs = make(map[string]struct{})
		for _, guild := range ready.Guilds {
			g.readyGuildIDs[guild.ID] = struct{}{}
		}
		appID := g.applicationID
		g.mu.Unlock()

		log.Printf("[Discord] Ready as %s (%s)", ready.User.Username, ready.User.ID)
		go registerSlashCommands(token, appID)

	case "RESUMED":
		debugLog("Session resumed successfully")

	case "MESSAGE_CREATE":
		var msg discordMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[Discord] Failed to parse MESSAGE_CREATE: %v", err)
			return
		}
		go g.handleMessageCreate(ctx, token, &msg)

	case "INTERACTION_CREATE":
		var interaction discordInteraction
		if err := json.Unmarshal(raw, &interaction); err != nil {
			log.Printf("[Discord] Failed to parse INTERACTION_CREATE: %v", err)
			return
		}
		go g.handleInteractionCreate(token, &interaction)

	case "GUILD_CREATE":
		var guild discordGuild
		if err := json.Unmarshal(raw, &guild); err != nil {
			log.Printf("[Discord] Failed to parse GUILD_CREATE: %v", err)
			return
		}
		go g.handleGuildCreate(token, &guild)
	}
}

// ---------------------------------------------------------------------------
// Gateway connection and reconnection
// ---------------------------------------------------------------------------

// connect establishes a WebSocket connection to the gateway and reads messages
// until the connection is closed or the context is cancelled.
func (g *Gateway) connect(ctx context.Context, token string) error {
	g.mu.Lock()
	connectURL := g.resumeURL
	if connectURL == "" {
		connectURL = defaultGateway
	}
	g.mu.Unlock()

	debugLog("Connecting to gateway: %s", connectURL)

	conn, _, err := websocket.Dial(ctx, connectURL, nil)
	if err != nil {
		return fmt.Errorf("discord: dial gateway: %w", err)
	}

	// Allow large messages from Discord gateway.
	conn.SetReadLimit(16 * 1024 * 1024)

	g.mu.Lock()
	g.conn = conn
	g.mu.Unlock()

	debugLog("Gateway WebSocket opened")

	// Read loop.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			g.mu.Lock()
			g.conn = nil
			g.mu.Unlock()
			return fmt.Errorf("discord: read: %w", err)
		}

		var payload gatewayPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			log.Printf("[Discord] Failed to parse gateway payload: %v", err)
			continue
		}

		g.handlePayload(ctx, token, &payload)
	}
}

// isFatalClose checks if a websocket close error contains a fatal close code.
func isFatalClose(err error) bool {
	if err == nil {
		return false
	}
	status := websocket.CloseStatus(err)
	if status == -1 {
		return false
	}
	return fatalCloseCodes[int(status)]
}

// run is the main gateway loop with auto-reconnection.
func (g *Gateway) run(ctx context.Context, token string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := g.connect(ctx, token)
		if err == nil {
			continue
		}

		// Check for context cancellation.
		if ctx.Err() != nil {
			return
		}

		// Fatal close codes: do not reconnect.
		if isFatalClose(err) {
			log.Printf("[Discord] Fatal close: %v. Not reconnecting.", err)
			return
		}

		debugLog("Connection lost: %v", err)

		// Determine reconnect strategy.
		g.mu.Lock()
		canResume := g.sessionID != "" && g.lastSequence != nil
		g.mu.Unlock()

		if canResume {
			debugLog("Attempting resume...")
			delay := time.Duration(1000+rand.IntN(2000)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		} else {
			// Full reconnect: clear session state.
			g.mu.Lock()
			g.sessionID = ""
			g.lastSequence = nil
			g.resumeURL = ""
			g.mu.Unlock()

			delay := time.Duration(3000+rand.IntN(4000)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Exported entry points
// ---------------------------------------------------------------------------

// StartGateway starts the gateway connection in a background goroutine.
// It is non-blocking and intended to be called by the daemon start command.
func StartGateway(debug bool) {
	debugEnabled = debug

	cfg := config.GetSettings().Discord

	ctx, cancel := context.WithCancel(context.Background())

	gw := NewGateway()
	gw.debug = debug
	gw.cancel = cancel

	// Store the gateway for StopGateway.
	activeGatewayMu.Lock()
	activeGateway = gw
	activeGatewayMu.Unlock()

	log.Println("Discord bot started (gateway)")
	log.Printf("  Allowed users: %s", formatAllowed(cfg.AllowedUserIds))
	if len(cfg.ListenChannels) > 0 {
		log.Printf("  Listen channels: %s", strings.Join(cfg.ListenChannels, ", "))
	}
	if debug {
		log.Println("  Debug: enabled")
	}

	runner.EnsureProjectClaudeMd()

	go gw.run(ctx, cfg.Token)
}

// StopGateway stops the active gateway connection and clears runtime state.
func StopGateway() {
	activeGatewayMu.Lock()
	gw := activeGateway
	activeGateway = nil
	activeGatewayMu.Unlock()

	if gw == nil {
		return
	}

	gw.mu.Lock()
	cancel := gw.cancel
	conn := gw.conn
	gw.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "Gateway stop requested")
	}

	gw.mu.Lock()
	gw.conn = nil
	gw.cancel = nil
	gw.resetState()
	gw.mu.Unlock()
}

// Standalone runs the Discord bot as a standalone process (blocking).
// Equivalent to `bun run src/index.ts discord`.
func Standalone() {
	if _, err := config.LoadSettings(); err != nil {
		log.Fatalf("[Discord] Failed to load settings: %v", err)
	}

	cfg := config.GetSettings().Discord
	if cfg.Token == "" {
		log.Fatal("Discord token not configured. Set discord.token in .claude/claudeclaw/settings.json")
	}

	runner.EnsureProjectClaudeMd()

	gw := NewGateway()
	gw.debug = debugEnabled

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Trap SIGTERM/SIGINT for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[Discord] Shutting down...")
		cancel()
		gw.mu.Lock()
		conn := gw.conn
		gw.mu.Unlock()
		if conn != nil {
			conn.Close(websocket.StatusNormalClosure, "Gateway stop requested")
		}
	}()

	log.Println("Discord bot started (gateway, standalone)")
	log.Printf("  Allowed users: %s", formatAllowed(cfg.AllowedUserIds))
	if debugEnabled {
		log.Println("  Debug: enabled")
	}

	gw.run(ctx, cfg.Token)
}

// ---------------------------------------------------------------------------
// Active gateway singleton (for StartGateway / StopGateway)
// ---------------------------------------------------------------------------

var (
	activeGateway   *Gateway
	activeGatewayMu sync.Mutex
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func formatAllowed(ids []string) string {
	if len(ids) == 0 {
		return "all"
	}
	return strings.Join(ids, ", ")
}
