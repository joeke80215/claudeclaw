package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

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
	apiBase     = "https://api.telegram.org/bot"
	fileAPIBase = "https://api.telegram.org/file/bot"
	maxMsgLen   = 4096
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type TelegramUser struct {
	ID        int    `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username,omitempty"`
}

type TelegramChat struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
}

type TelegramPhoto struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize *int   `json:"file_size,omitempty"`
}

type TelegramDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileSize *int   `json:"file_size,omitempty"`
}

type TelegramVoice struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type,omitempty"`
	Duration int    `json:"duration,omitempty"`
	FileSize *int   `json:"file_size,omitempty"`
}

type TelegramAudio struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type,omitempty"`
	Duration int    `json:"duration,omitempty"`
	FileName string `json:"file_name,omitempty"`
	FileSize *int   `json:"file_size,omitempty"`
}

type TelegramEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type TelegramMessage struct {
	MessageID       int               `json:"message_id"`
	From            *TelegramUser     `json:"from,omitempty"`
	Chat            TelegramChat      `json:"chat"`
	MessageThreadID *int              `json:"message_thread_id,omitempty"`
	Text            string            `json:"text,omitempty"`
	Caption         string            `json:"caption,omitempty"`
	Photo           []TelegramPhoto   `json:"photo,omitempty"`
	Document        *TelegramDocument `json:"document,omitempty"`
	Voice           *TelegramVoice    `json:"voice,omitempty"`
	Audio           *TelegramAudio    `json:"audio,omitempty"`
	Entities        []TelegramEntity  `json:"entities,omitempty"`
	CaptionEntities []TelegramEntity  `json:"caption_entities,omitempty"`
	ReplyToMessage  *TelegramMessage  `json:"reply_to_message,omitempty"`
}

type TelegramChatMember struct {
	User   TelegramUser `json:"user"`
	Status string       `json:"status"`
}

type TelegramMyChatMemberUpdate struct {
	Chat          TelegramChat       `json:"chat"`
	From          TelegramUser       `json:"from"`
	OldChatMember TelegramChatMember `json:"old_chat_member"`
	NewChatMember TelegramChatMember `json:"new_chat_member"`
}

type TelegramCallbackQuery struct {
	ID      string           `json:"id"`
	From    TelegramUser     `json:"from"`
	Message *TelegramMessage `json:"message,omitempty"`
	Data    string           `json:"data,omitempty"`
}

type TelegramUpdate struct {
	UpdateID         int                         `json:"update_id"`
	Message          *TelegramMessage            `json:"message,omitempty"`
	EditedMessage    *TelegramMessage            `json:"edited_message,omitempty"`
	ChannelPost      *TelegramMessage            `json:"channel_post,omitempty"`
	EditedChannelPost *TelegramMessage           `json:"edited_channel_post,omitempty"`
	MyChatMember     *TelegramMyChatMemberUpdate `json:"my_chat_member,omitempty"`
	CallbackQuery    *TelegramCallbackQuery      `json:"callback_query,omitempty"`
}

type TelegramMe struct {
	ID                      int    `json:"id"`
	Username                string `json:"username,omitempty"`
	CanReadAllGroupMessages bool   `json:"can_read_all_group_messages,omitempty"`
}

type TelegramFile struct {
	FilePath string `json:"file_path,omitempty"`
}

type telegramAPIResponse[T any] struct {
	OK     bool `json:"ok"`
	Result T    `json:"result"`
}

type botCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// ---------------------------------------------------------------------------
// Module state
// ---------------------------------------------------------------------------

var (
	debug       bool
	botUsername  string
	botID       int
	mu          sync.Mutex // guards botUsername and botID
	messageSem  = make(chan struct{}, 1) // semaphore: process one message at a time
)

func debugLog(format string, args ...interface{}) {
	if !debug {
		return
	}
	log.Printf("[Telegram][debug] "+format, args...)
}

// ---------------------------------------------------------------------------
// Generic API caller
// ---------------------------------------------------------------------------

func callAPI[T any](token, method string, body interface{}) (T, error) {
	var zero T
	url := fmt.Sprintf("%s%s/%s", apiBase, token, method)

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return zero, fmt.Errorf("marshal body for %s: %w", method, err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, url, reqBody)
	if err != nil {
		return zero, fmt.Errorf("create request for %s: %w", method, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("telegram API %s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return zero, fmt.Errorf("telegram API %s: %d %s: %s", method, resp.StatusCode, resp.Status, string(respBody))
	}

	var result telegramAPIResponse[T]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return zero, fmt.Errorf("decode response for %s: %w", method, err)
	}
	return result.Result, nil
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

var unicodeDashRe = regexp.MustCompile(`[\x{2010}-\x{2015}\x{2212}]`)

func normalizeTelegramText(text string) string {
	return unicodeDashRe.ReplaceAllString(text, "-")
}

func getMessageTextAndEntities(msg *TelegramMessage) (string, []TelegramEntity) {
	if msg.Text != "" {
		return normalizeTelegramText(msg.Text), msg.Entities
	}
	if msg.Caption != "" {
		return normalizeTelegramText(msg.Caption), msg.CaptionEntities
	}
	return "", nil
}

func isImageDocument(doc *TelegramDocument) bool {
	return doc != nil && strings.HasPrefix(doc.MimeType, "image/")
}

func isAudioDocument(doc *TelegramDocument) bool {
	return doc != nil && strings.HasPrefix(doc.MimeType, "audio/")
}

func pickLargestPhoto(photos []TelegramPhoto) TelegramPhoto {
	best := photos[0]
	bestSize := photoSize(best)
	for _, p := range photos[1:] {
		s := photoSize(p)
		if s > bestSize {
			best = p
			bestSize = s
		}
	}
	return best
}

func photoSize(p TelegramPhoto) int {
	if p.FileSize != nil {
		return *p.FileSize
	}
	return p.Width * p.Height
}

func extensionFromMimeType(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/bmp":
		return ".bmp"
	default:
		return ""
	}
}

func extensionFromAudioMimeType(mime string) string {
	switch mime {
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/webm":
		return ".webm"
	default:
		return ""
	}
}

func extractTelegramCommand(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	firstToken := strings.Fields(trimmed)[0]
	if !strings.HasPrefix(firstToken, "/") {
		return ""
	}
	// Strip @botname suffix
	cmd := strings.SplitN(firstToken, "@", 2)[0]
	return strings.ToLower(cmd)
}

var reactRe = regexp.MustCompile(`(?i)\[react:([^\]\r\n]+)\]`)

func extractReactionDirective(text string) (cleanedText, reactionEmoji string) {
	var emoji string
	cleaned := reactRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := reactRe.FindStringSubmatch(match)
		if emoji == "" && len(sub) > 1 {
			candidate := strings.TrimSpace(sub[1])
			if candidate != "" {
				emoji = candidate
			}
		}
		return ""
	})
	// Clean up trailing whitespace on lines and collapse excess blank lines
	cleaned = regexp.MustCompile(`[ \t]+\n`).ReplaceAllString(cleaned, "\n")
	cleaned = regexp.MustCompile(`\n{3,}`).ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, emoji
}

// ---------------------------------------------------------------------------
// Markdown → Telegram HTML conversion
// ---------------------------------------------------------------------------

func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}

	// 1. Extract and protect code blocks
	var codeBlocks []string
	codeBlockRe := regexp.MustCompile("(?s)```\\w*\n?(.*?)```")
	text = codeBlockRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := codeBlockRe.FindStringSubmatch(match)
		code := ""
		if len(sub) > 1 {
			code = sub[1]
		}
		idx := len(codeBlocks)
		codeBlocks = append(codeBlocks, code)
		return fmt.Sprintf("\x00CB%d\x00", idx)
	})

	// 2. Extract and protect inline code
	var inlineCodes []string
	inlineCodeRe := regexp.MustCompile("`([^`]+)`")
	text = inlineCodeRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := inlineCodeRe.FindStringSubmatch(match)
		code := ""
		if len(sub) > 1 {
			code = sub[1]
		}
		idx := len(inlineCodes)
		inlineCodes = append(inlineCodes, code)
		return fmt.Sprintf("\x00IC%d\x00", idx)
	})

	// 3. Strip markdown headers
	headerRe := regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	text = headerRe.ReplaceAllString(text, "$1")

	// 4. Strip blockquotes
	blockquoteRe := regexp.MustCompile(`(?m)^>\s*(.*)$`)
	text = blockquoteRe.ReplaceAllString(text, "$1")

	// 5. Escape HTML special characters
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")

	// 6. Links [text](url) - before bold/italic to handle nested cases
	linkRe := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	text = linkRe.ReplaceAllString(text, `<a href="$2">$1</a>`)

	// 7. Bold **text** or __text__
	boldStarRe := regexp.MustCompile(`\*\*(.+?)\*\*`)
	text = boldStarRe.ReplaceAllString(text, "<b>$1</b>")
	boldUnderRe := regexp.MustCompile(`__(.+?)__`)
	text = boldUnderRe.ReplaceAllString(text, "<b>$1</b>")

	// 8. Italic _text_ (avoid matching inside words)
	italicRe := regexp.MustCompile(`(?:^|[^a-zA-Z0-9])_([^_]+)_(?:[^a-zA-Z0-9]|$)`)
	text = italicRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := italicRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		// Preserve surrounding characters
		prefix := ""
		suffix := ""
		inner := sub[1]
		full := match
		startIdx := strings.Index(full, "_"+inner+"_")
		if startIdx > 0 {
			prefix = full[:startIdx]
		}
		endIdx := startIdx + len("_"+inner+"_")
		if endIdx < len(full) {
			suffix = full[endIdx:]
		}
		return prefix + "<i>" + inner + "</i>" + suffix
	})

	// 9. Strikethrough ~~text~~
	strikeRe := regexp.MustCompile(`~~(.+?)~~`)
	text = strikeRe.ReplaceAllString(text, "<s>$1</s>")

	// 10. Bullet lists
	bulletRe := regexp.MustCompile(`(?m)^[-*]\s+`)
	text = bulletRe.ReplaceAllString(text, "\u2022 ")

	// 11. Restore inline code with HTML tags
	for i, code := range inlineCodes {
		escaped := strings.ReplaceAll(code, "&", "&amp;")
		escaped = strings.ReplaceAll(escaped, "<", "&lt;")
		escaped = strings.ReplaceAll(escaped, ">", "&gt;")
		text = strings.Replace(text, fmt.Sprintf("\x00IC%d\x00", i), "<code>"+escaped+"</code>", 1)
	}

	// 12. Restore code blocks with HTML tags
	for i, code := range codeBlocks {
		escaped := strings.ReplaceAll(code, "&", "&amp;")
		escaped = strings.ReplaceAll(escaped, "<", "&lt;")
		escaped = strings.ReplaceAll(escaped, ">", "&gt;")
		text = strings.Replace(text, fmt.Sprintf("\x00CB%d\x00", i), "<pre><code>"+escaped+"</code></pre>", 1)
	}

	return text
}

// ---------------------------------------------------------------------------
// Typing indicator (with context cancellation fix)
// ---------------------------------------------------------------------------

func startTyping(ctx context.Context, token string, chatID int, threadID *int) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		sendTyping(token, chatID, threadID)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sendTyping(token, chatID, threadID)
			}
		}
	}()
	return cancel
}

func sendTyping(token string, chatID int, threadID *int) {
	body := map[string]interface{}{
		"chat_id": chatID,
		"action":  "typing",
	}
	if threadID != nil {
		body["message_thread_id"] = *threadID
	}
	_, _ = callAPI[json.RawMessage](token, "sendChatAction", body)
}

// ---------------------------------------------------------------------------
// SendMessage - exported for heartbeat forwarding
// ---------------------------------------------------------------------------

func SendMessage(token string, chatID int, text string, threadID *int) error {
	normalized := normalizeTelegramText(text)
	normalized = reactRe.ReplaceAllString(normalized, "")
	html := markdownToTelegramHTML(normalized)

	for i := 0; i < len(html); i += maxMsgLen {
		end := i + maxMsgLen
		if end > len(html) {
			end = len(html)
		}
		chunk := html[i:end]

		body := map[string]interface{}{
			"chat_id":    chatID,
			"text":       chunk,
			"parse_mode": "HTML",
		}
		if threadID != nil {
			body["message_thread_id"] = *threadID
		}

		_, err := callAPI[json.RawMessage](token, "sendMessage", body)
		if err != nil {
			// Fallback to plain text if HTML parsing fails
			plainEnd := i + maxMsgLen
			if plainEnd > len(normalized) {
				plainEnd = len(normalized)
			}
			plainChunk := normalized[i:plainEnd]
			plainBody := map[string]interface{}{
				"chat_id": chatID,
				"text":    plainChunk,
			}
			if threadID != nil {
				plainBody["message_thread_id"] = *threadID
			}
			if _, err2 := callAPI[json.RawMessage](token, "sendMessage", plainBody); err2 != nil {
				return fmt.Errorf("sendMessage fallback failed: %w", err2)
			}
		}
	}
	return nil
}

func sendReaction(token string, chatID, messageID int, emoji string) error {
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"reaction":   []map[string]string{{"type": "emoji", "emoji": emoji}},
	}
	_, err := callAPI[json.RawMessage](token, "setMessageReaction", body)
	return err
}

// ---------------------------------------------------------------------------
// Group trigger detection
// ---------------------------------------------------------------------------

func groupTriggerReason(msg *TelegramMessage) string {
	mu.Lock()
	currentBotID := botID
	currentBotUsername := botUsername
	mu.Unlock()

	if currentBotID != 0 && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil && msg.ReplyToMessage.From.ID == currentBotID {
		return "reply_to_bot"
	}

	text, entities := getMessageTextAndEntities(msg)
	if text == "" {
		return ""
	}

	lowerText := strings.ToLower(text)
	if currentBotUsername != "" && strings.Contains(lowerText, "@"+strings.ToLower(currentBotUsername)) {
		return "text_contains_mention"
	}

	for _, entity := range entities {
		if entity.Offset+entity.Length > len([]rune(text)) {
			continue
		}
		value := string([]rune(text)[entity.Offset : entity.Offset+entity.Length])

		if entity.Type == "mention" && currentBotUsername != "" && strings.EqualFold(value, "@"+currentBotUsername) {
			return "mention_entity_matches_bot"
		}
		if entity.Type == "mention" && currentBotUsername == "" {
			return "mention_entity_before_botname_loaded"
		}
		if entity.Type == "bot_command" {
			if !strings.Contains(value, "@") {
				return "bare_bot_command"
			}
			if currentBotUsername == "" {
				return "scoped_command_before_botname_loaded"
			}
			if strings.HasSuffix(strings.ToLower(value), "@"+strings.ToLower(currentBotUsername)) {
				return "scoped_command_matches_bot"
			}
		}
	}

	return ""
}

// ---------------------------------------------------------------------------
// File downloads
// ---------------------------------------------------------------------------

func inboxDir() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".claude", "claudeclaw", "inbox", "telegram")
}

func downloadImageFromMessage(token string, msg *TelegramMessage) (string, error) {
	var fileID string
	var docName, docMime string

	if len(msg.Photo) > 0 {
		largest := pickLargestPhoto(msg.Photo)
		fileID = largest.FileID
	} else if isImageDocument(msg.Document) {
		fileID = msg.Document.FileID
		docName = msg.Document.FileName
		docMime = msg.Document.MimeType
	}

	if fileID == "" {
		return "", fmt.Errorf("no image found in message")
	}

	fileMeta, err := callAPI[TelegramFile](token, "getFile", map[string]string{"file_id": fileID})
	if err != nil {
		return "", fmt.Errorf("getFile: %w", err)
	}
	if fileMeta.FilePath == "" {
		return "", fmt.Errorf("getFile returned empty file_path")
	}

	downloadURL := fmt.Sprintf("%s%s/%s", fileAPIBase, token, fileMeta.FilePath)
	data, err := downloadFile(downloadURL)
	if err != nil {
		return "", err
	}

	dir := inboxDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir inbox: %w", err)
	}

	remoteExt := filepath.Ext(fileMeta.FilePath)
	docExt := filepath.Ext(docName)
	mimeExt := extensionFromMimeType(docMime)
	ext := firstNonEmpty(remoteExt, docExt, mimeExt, ".jpg")

	filename := fmt.Sprintf("%d-%d-%d%s", msg.Chat.ID, msg.MessageID, time.Now().UnixMilli(), ext)
	localPath := filepath.Join(dir, filename)
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write image file: %w", err)
	}
	return localPath, nil
}

func downloadVoiceFromMessage(token string, msg *TelegramMessage) (string, error) {
	var fileID string
	var mime string
	var audioDocName, audioFileName string

	switch {
	case msg.Voice != nil:
		fileID = msg.Voice.FileID
		mime = msg.Voice.MimeType
	case msg.Audio != nil:
		fileID = msg.Audio.FileID
		mime = msg.Audio.MimeType
		audioFileName = msg.Audio.FileName
	case isAudioDocument(msg.Document):
		fileID = msg.Document.FileID
		mime = msg.Document.MimeType
		audioDocName = msg.Document.FileName
	}

	if fileID == "" {
		return "", fmt.Errorf("no voice/audio found in message")
	}

	debugLog("Voice download: fileId=%s mime=%s", fileID, mime)

	fileMeta, err := callAPI[TelegramFile](token, "getFile", map[string]string{"file_id": fileID})
	if err != nil {
		return "", fmt.Errorf("getFile: %w", err)
	}
	if fileMeta.FilePath == "" {
		return "", fmt.Errorf("getFile returned empty file_path")
	}

	downloadURL := fmt.Sprintf("%s%s/%s", fileAPIBase, token, fileMeta.FilePath)
	data, err := downloadFile(downloadURL)
	if err != nil {
		return "", err
	}

	dir := inboxDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir inbox: %w", err)
	}

	remoteExt := filepath.Ext(fileMeta.FilePath)
	docExt := filepath.Ext(audioDocName)
	audioExt := filepath.Ext(audioFileName)
	mimeExt := extensionFromAudioMimeType(mime)
	ext := firstNonEmpty(remoteExt, docExt, audioExt, mimeExt, ".ogg")

	filename := fmt.Sprintf("%d-%d-%d%s", msg.Chat.ID, msg.MessageID, time.Now().UnixMilli(), ext)
	localPath := filepath.Join(dir, filename)
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write voice file: %w", err)
	}

	debugLog("Voice download: wrote %d bytes to %s ext=%s", len(data), localPath, ext)
	return localPath, nil
}

func downloadFile(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download file: HTTP %d %s", resp.StatusCode, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read file body: %w", err)
	}
	return data, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Callback query handler
// ---------------------------------------------------------------------------

func handleCallbackQuery(query *TelegramCallbackQuery) {
	cfg := config.GetSettings()
	token := cfg.Telegram.Token
	data := query.Data

	// Secretary pattern: "sec_yes_<8hex>" or "sec_no_<8hex>"
	secRe := regexp.MustCompile(`^sec_(yes|no)_([0-9a-f]{8})$`)
	if matches := secRe.FindStringSubmatch(data); matches != nil {
		action := matches[1]
		pendingID := matches[2]
		answerText := "Warning: Server error"

		func() {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:9999/confirm/%s/%s", pendingID, action))
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var result struct {
				OK bool `json:"ok"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return
			}
			if action == "yes" && result.OK {
				answerText = "Sent!"
			} else if result.OK {
				answerText = "Dismissed"
			} else {
				answerText = "Not found"
			}

			if query.Message != nil {
				statusLine := "\n\nSent"
				if action != "yes" {
					statusLine = "\n\nDismissed"
				}
				oldText := query.Message.Text
				// Remove previous reply status
				replyRe := regexp.MustCompile(`(?s)\n\nReply:.*$`)
				newText := replyRe.ReplaceAllString(oldText, statusLine)
				_, _ = callAPI[json.RawMessage](token, "editMessageText", map[string]interface{}{
					"chat_id":    query.Message.Chat.ID,
					"message_id": query.Message.MessageID,
					"text":       newText,
				})
			}
		}()

		_, _ = callAPI[json.RawMessage](token, "answerCallbackQuery", map[string]interface{}{
			"callback_query_id": query.ID,
			"text":              answerText,
		})
		return
	}

	// Default: ack with no text
	_, _ = callAPI[json.RawMessage](token, "answerCallbackQuery", map[string]interface{}{
		"callback_query_id": query.ID,
	})
}

// ---------------------------------------------------------------------------
// my_chat_member handler
// ---------------------------------------------------------------------------

func handleMyChatMember(update *TelegramMyChatMemberUpdate) {
	cfg := config.GetSettings()
	token := cfg.Telegram.Token
	chat := update.Chat

	mu.Lock()
	if botUsername == "" && update.NewChatMember.User.Username != "" {
		botUsername = update.NewChatMember.User.Username
	}
	if botID == 0 {
		botID = update.NewChatMember.User.ID
	}
	mu.Unlock()

	oldStatus := update.OldChatMember.Status
	newStatus := update.NewChatMember.Status
	isGroup := chat.Type == "group" || chat.Type == "supergroup"
	wasOut := oldStatus == "left" || oldStatus == "kicked"
	isIn := newStatus == "member" || newStatus == "administrator"

	if !isGroup || !wasOut || !isIn {
		return
	}

	chatName := chat.Title
	if chatName == "" {
		chatName = fmt.Sprintf("%d", chat.ID)
	}
	log.Printf("[Telegram] Added to %s: %s (%d) by %d", chat.Type, chatName, chat.ID, update.From.ID)

	addedBy := update.From.Username
	if addedBy == "" {
		addedBy = fmt.Sprintf("%s (%d)", update.From.FirstName, update.From.ID)
	}

	eventPrompt := fmt.Sprintf(
		"[Telegram system event] I was added to a %s.\nGroup title: %s\nGroup id: %d\nAdded by: %s\nWrite a short first message for the group. It should confirm I was added and explain how to trigger me.",
		chat.Type, chatName, chat.ID, addedBy,
	)

	result, err := runner.Run("telegram", eventPrompt)
	if err != nil || result.ExitCode != 0 {
		_ = SendMessage(token, chat.ID, "I was added to this group. Mention me with a command to start.", nil)
		return
	}
	msg := result.Stdout
	if msg == "" {
		msg = "I was added to this group."
	}
	_ = SendMessage(token, chat.ID, msg, nil)
}

// ---------------------------------------------------------------------------
// Message handler
// ---------------------------------------------------------------------------

func handleMessage(ctx context.Context, msg *TelegramMessage) {
	cfg := config.GetSettings()
	token := cfg.Telegram.Token
	userID := 0
	if msg.From != nil {
		userID = msg.From.ID
	}
	chatID := msg.Chat.ID
	threadID := msg.MessageThreadID
	text, _ := getMessageTextAndEntities(msg)
	chatType := msg.Chat.Type
	isPrivate := chatType == "private"
	isGroup := chatType == "group" || chatType == "supergroup"
	hasImage := len(msg.Photo) > 0 || isImageDocument(msg.Document)
	hasVoice := msg.Voice != nil || msg.Audio != nil || isAudioDocument(msg.Document)

	if !isPrivate && !isGroup {
		return
	}

	triggerReason := "private_chat"
	if isGroup {
		triggerReason = groupTriggerReason(msg)
		if triggerReason == "" {
			debugLog("Skip group message chat=%d from=%d reason=no_trigger text=\"%.80s\"", chatID, userID, text)
			return
		}
	}
	debugLog("Handle message chat=%d type=%s from=%d reason=%s text=\"%.80s\"", chatID, chatType, userID, triggerReason, text)

	// Authorization check
	if userID != 0 && len(cfg.Telegram.AllowedUserIds) > 0 {
		allowed := false
		for _, id := range cfg.Telegram.AllowedUserIds {
			if id == userID {
				allowed = true
				break
			}
		}
		if !allowed {
			if isPrivate {
				_ = SendMessage(token, chatID, "Unauthorized.", threadID)
			} else {
				log.Printf("[Telegram] Ignored group message from unauthorized user %d in chat %d", userID, chatID)
				debugLog("Skip group message chat=%d from=%d reason=unauthorized_user", chatID, userID)
			}
			return
		}
	}

	if strings.TrimSpace(text) == "" && !hasImage && !hasVoice {
		debugLog("Skip message chat=%d from=%d reason=empty_text", chatID, userID)
		return
	}

	command := extractTelegramCommand(text)

	if command == "/start" {
		_ = SendMessage(token, chatID, "Hello! Send me a message and I'll respond using Claude.\nUse /reset to start a fresh session.", threadID)
		return
	}

	if command == "/reset" {
		_ = sessions.ResetSession()
		_ = SendMessage(token, chatID, "Global session reset. Next message starts fresh.", threadID)
		return
	}

	label := fmt.Sprintf("%d", userID)
	if msg.From != nil && msg.From.Username != "" {
		label = msg.From.Username
	}

	mediaParts := []string{}
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

	truncated := text
	if len(truncated) > 60 {
		truncated = truncated[:60] + "..."
	}
	log.Printf("[%s] Telegram %s%s: \"%s\"", time.Now().Format("15:04:05"), label, mediaSuffix, truncated)

	// Start typing indicator with cancellation
	stopTyping := startTyping(ctx, token, chatID, threadID)
	defer stopTyping()

	// Download image if present
	var imagePath string
	if hasImage {
		path, err := downloadImageFromMessage(token, msg)
		if err != nil {
			log.Printf("[Telegram] Failed to download image for %s: %v", label, err)
		} else {
			imagePath = path
		}
	}

	// Download and transcribe voice if present
	var voicePath, voiceTranscript string
	if hasVoice {
		path, err := downloadVoiceFromMessage(token, msg)
		if err != nil {
			log.Printf("[Telegram] Failed to download voice for %s: %v", label, err)
		} else {
			voicePath = path
		}

		if voicePath != "" {
			debugLog("Voice file saved: path=%s", voicePath)
			transcript, err := whisper.TranscribeAudioToText(voicePath, debug)
			if err != nil {
				log.Printf("[Telegram] Failed to transcribe voice for %s: %v", label, err)
			} else {
				voiceTranscript = transcript
			}
		}
	}

	// Skill routing
	var skillContext string
	if command != "" && command != "/start" && command != "/reset" {
		resolved, err := skills.ResolveSkillPrompt(command)
		if err != nil {
			debugLog("Skill resolution failed for %s: %v", command, err)
		} else if resolved != "" {
			skillContext = resolved
			debugLog("Skill resolved for %s: %d chars", command, len(skillContext))
		}
	}

	// Build prompt
	promptParts := []string{fmt.Sprintf("[Telegram from %s]", label)}
	if threadID != nil {
		promptParts = append(promptParts, fmt.Sprintf("[thread:%d]", *threadID))
	}

	if skillContext != "" {
		args := strings.TrimSpace(text)
		if command != "" {
			args = strings.TrimSpace(args[len(command):])
		}
		promptParts = append(promptParts, fmt.Sprintf("<command-name>%s</command-name>", command))
		promptParts = append(promptParts, skillContext)
		if args != "" {
			promptParts = append(promptParts, "User arguments: "+args)
		}
	} else if strings.TrimSpace(text) != "" {
		promptParts = append(promptParts, "Message: "+text)
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
	result, err := runner.RunUserMessage("telegram", prefixedPrompt)
	if err != nil {
		log.Printf("[Telegram] Error for %s: %v", label, err)
		_ = SendMessage(token, chatID, fmt.Sprintf("Error: %v", err), threadID)
		return
	}

	if result.ExitCode != 0 {
		errText := result.Stderr
		if errText == "" {
			errText = "Unknown error"
		}
		_ = SendMessage(token, chatID, fmt.Sprintf("Error (exit %d): %s", result.ExitCode, errText), threadID)
	} else {
		stdout := result.Stdout
		if stdout == "" {
			stdout = "(empty response)"
		}
		cleanedText, reactionEmoji := extractReactionDirective(stdout)
		if reactionEmoji != "" {
			if err := sendReaction(token, chatID, msg.MessageID, reactionEmoji); err != nil {
				log.Printf("[Telegram] Failed to send reaction for %s: %v", label, err)
			}
		}
		if cleanedText == "" {
			cleanedText = "(empty response)"
		}
		_ = SendMessage(token, chatID, cleanedText, threadID)
	}
}

// ---------------------------------------------------------------------------
// Bot command registration
// ---------------------------------------------------------------------------

func registerBotCommands(token string) {
	allSkills, err := skills.ListSkills()
	if err != nil {
		log.Printf("[Telegram] Failed to list skills for command registration: %v", err)
		return
	}

	commands := []botCommand{
		{Command: "start", Description: "Show welcome message"},
		{Command: "reset", Description: "Reset session and start fresh"},
	}

	sanitizeRe := regexp.MustCompile(`[^a-z0-9_]`)
	replaceRe := regexp.MustCompile(`[-.:]+`)

	for _, skill := range allSkills {
		cmd := strings.ToLower(skill.Name)
		cmd = replaceRe.ReplaceAllString(cmd, "_")
		cmd = sanitizeRe.ReplaceAllString(cmd, "")
		if len(cmd) > 32 {
			cmd = cmd[:32]
		}
		if cmd == "" || cmd == "start" || cmd == "reset" {
			continue
		}
		if len(cmd) > 30 {
			continue
		}
		desc := skill.Description
		if len(desc) < 3 {
			desc = "Run " + skill.Name + " skill"
		}
		if len(desc) > 256 {
			desc = desc[:256]
		}
		commands = append(commands, botCommand{Command: cmd, Description: desc})
	}

	if len(commands) > 100 {
		commands = commands[:100]
	}

	_, err = callAPI[json.RawMessage](token, "setMyCommands", map[string]interface{}{
		"commands": commands,
	})
	if err != nil {
		log.Printf("[Telegram] Failed to register commands: %v", err)
		return
	}

	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = "/" + c.Command
	}
	log.Printf("  Commands registered: %d (%s)", len(commands), strings.Join(names, ", "))
}

// ---------------------------------------------------------------------------
// Polling loop
// ---------------------------------------------------------------------------

func poll(ctx context.Context) {
	cfg := config.GetSettings()
	token := cfg.Telegram.Token
	offset := 0

	// Get bot info
	me, err := callAPI[TelegramMe](token, "getMe", nil)
	if err != nil {
		log.Printf("[Telegram] getMe failed: %v", err)
	} else {
		mu.Lock()
		botUsername = me.Username
		botID = me.ID
		mu.Unlock()
		if me.Username != "" {
			log.Printf("  Bot: @%s", me.Username)
		} else {
			log.Printf("  Bot: %d", me.ID)
		}
		if me.CanReadAllGroupMessages {
			log.Printf("  Group privacy: disabled (reads all messages)")
		} else {
			log.Printf("  Group privacy: enabled (commands & mentions only)")
		}
	}

	log.Println("Telegram bot started (long polling)")
	log.Printf("  Allowed users: %s", func() string {
		if len(cfg.Telegram.AllowedUserIds) == 0 {
			return "all"
		}
		parts := make([]string, len(cfg.Telegram.AllowedUserIds))
		for i, id := range cfg.Telegram.AllowedUserIds {
			parts[i] = fmt.Sprintf("%d", id)
		}
		return strings.Join(parts, ", ")
	}())
	if debug {
		log.Println("  Debug: enabled")
	}

	// Register commands (non-blocking)
	go registerBotCommands(token)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		data, err := callAPI[[]TelegramUpdate](token, "getUpdates", map[string]interface{}{
			"offset":          offset,
			"timeout":         30,
			"allowed_updates": []string{"message", "my_chat_member", "callback_query"},
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			log.Printf("[Telegram] Poll error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if len(data) == 0 {
			continue
		}

		for _, update := range data {
			debugLog("Update %d keys: message=%v edited=%v channel=%v my_chat_member=%v callback=%v",
				update.UpdateID,
				update.Message != nil,
				update.EditedMessage != nil,
				update.ChannelPost != nil,
				update.MyChatMember != nil,
				update.CallbackQuery != nil,
			)
			offset = update.UpdateID + 1

			// Collect all incoming messages
			incoming := []*TelegramMessage{}
			if update.Message != nil {
				incoming = append(incoming, update.Message)
			}
			if update.EditedMessage != nil {
				incoming = append(incoming, update.EditedMessage)
			}
			if update.ChannelPost != nil {
				incoming = append(incoming, update.ChannelPost)
			}
			if update.EditedChannelPost != nil {
				incoming = append(incoming, update.EditedChannelPost)
			}

			// Process messages sequentially using semaphore
			for _, m := range incoming {
				messageSem <- struct{}{} // acquire
				func(msg *TelegramMessage) {
					defer func() { <-messageSem }() // release
					handleMessage(ctx, msg)
				}(m)
			}

			if update.MyChatMember != nil {
				go func(u *TelegramMyChatMemberUpdate) {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[Telegram] my_chat_member panic: %v", r)
						}
					}()
					handleMyChatMember(u)
				}(update.MyChatMember)
			}

			if update.CallbackQuery != nil {
				go func(q *TelegramCallbackQuery) {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[Telegram] callback_query panic: %v", r)
						}
					}()
					handleCallbackQuery(q)
				}(update.CallbackQuery)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Exported entry points
// ---------------------------------------------------------------------------

// StartPolling starts the Telegram polling loop in a background goroutine.
// It is called by the daemon start command when a Telegram token is configured.
func StartPolling(debugMode bool) {
	debug = debugMode

	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	runner.EnsureProjectClaudeMd()

	go poll(ctx)
}

// Telegram is the standalone entry point for running the Telegram bot
// (equivalent to `bun run src/index.ts telegram`). It blocks until interrupted.
func Telegram() {
	if _, err := config.LoadSettings(); err != nil {
		log.Fatalf("[Telegram] Failed to load settings: %v", err)
	}
	runner.EnsureProjectClaudeMd()

	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	poll(ctx)
}
