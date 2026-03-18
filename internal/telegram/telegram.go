// Package telegram implements a Telegram bot using github.com/go-telegram/bot.
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

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/claudeclaw/claudeclaw/internal/config"
	"github.com/claudeclaw/claudeclaw/internal/runner"
	"github.com/claudeclaw/claudeclaw/internal/sessions"
	"github.com/claudeclaw/claudeclaw/internal/skills"
	"github.com/claudeclaw/claudeclaw/internal/whisper"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const maxMsgLen = 4096

// ---------------------------------------------------------------------------
// Module state
// ---------------------------------------------------------------------------

var (
	debugMode  bool
	botUsername string
	botIDVal   int64
	mu         sync.Mutex // guards botUsername and botIDVal

	// botInstance holds the *tgbot.Bot so SendMessage (exported) can use it.
	botInstance *tgbot.Bot
)

func debugLog(format string, args ...interface{}) {
	if !debugMode {
		return
	}
	log.Printf("[Telegram][debug] "+format, args...)
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

var unicodeDashRe = regexp.MustCompile(`[\x{2010}-\x{2015}\x{2212}]`)

func normalizeTelegramText(text string) string {
	return unicodeDashRe.ReplaceAllString(text, "-")
}

func getMessageTextAndEntities(msg *models.Message) (string, []models.MessageEntity) {
	if msg.Text != "" {
		return normalizeTelegramText(msg.Text), msg.Entities
	}
	if msg.Caption != "" {
		return normalizeTelegramText(msg.Caption), msg.CaptionEntities
	}
	return "", nil
}

func isImageDocument(doc *models.Document) bool {
	return doc != nil && strings.HasPrefix(doc.MimeType, "image/")
}

func isAudioDocument(doc *models.Document) bool {
	return doc != nil && strings.HasPrefix(doc.MimeType, "audio/")
}

func pickLargestPhoto(photos []models.PhotoSize) models.PhotoSize {
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

func photoSize(p models.PhotoSize) int {
	if p.FileSize > 0 {
		return p.FileSize
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
	cmd := strings.SplitN(firstToken, "@", 2)[0]
	return strings.ToLower(cmd)
}

var reactRe = regexp.MustCompile(`(?i)\[react:([^\]\r\n]+)\]`)
var trailingSpaceRe = regexp.MustCompile(`[ \t]+\n`)
var multiBlankRe = regexp.MustCompile(`\n{3,}`)

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
	cleaned = trailingSpaceRe.ReplaceAllString(cleaned, "\n")
	cleaned = multiBlankRe.ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, emoji
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// ---------------------------------------------------------------------------
// Markdown -> Telegram HTML conversion
// ---------------------------------------------------------------------------

func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}

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

	headerRe := regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	text = headerRe.ReplaceAllString(text, "$1")

	blockquoteRe := regexp.MustCompile(`(?m)^>\s*(.*)$`)
	text = blockquoteRe.ReplaceAllString(text, "$1")

	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")

	linkRe := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	text = linkRe.ReplaceAllString(text, `<a href="$2">$1</a>`)

	boldStarRe := regexp.MustCompile(`\*\*(.+?)\*\*`)
	text = boldStarRe.ReplaceAllString(text, "<b>$1</b>")
	boldUnderRe := regexp.MustCompile(`__(.+?)__`)
	text = boldUnderRe.ReplaceAllString(text, "<b>$1</b>")

	italicRe := regexp.MustCompile(`(?:^|[^a-zA-Z0-9])_([^_]+)_(?:[^a-zA-Z0-9]|$)`)
	text = italicRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := italicRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		inner := sub[1]
		full := match
		startIdx := strings.Index(full, "_"+inner+"_")
		prefix := ""
		suffix := ""
		if startIdx > 0 {
			prefix = full[:startIdx]
		}
		endIdx := startIdx + len("_"+inner+"_")
		if endIdx < len(full) {
			suffix = full[endIdx:]
		}
		return prefix + "<i>" + inner + "</i>" + suffix
	})

	strikeRe := regexp.MustCompile(`~~(.+?)~~`)
	text = strikeRe.ReplaceAllString(text, "<s>$1</s>")

	bulletRe := regexp.MustCompile(`(?m)^[-*]\s+`)
	text = bulletRe.ReplaceAllString(text, "\u2022 ")

	for i, code := range inlineCodes {
		escaped := strings.ReplaceAll(code, "&", "&amp;")
		escaped = strings.ReplaceAll(escaped, "<", "&lt;")
		escaped = strings.ReplaceAll(escaped, ">", "&gt;")
		text = strings.Replace(text, fmt.Sprintf("\x00IC%d\x00", i), "<code>"+escaped+"</code>", 1)
	}

	for i, code := range codeBlocks {
		escaped := strings.ReplaceAll(code, "&", "&amp;")
		escaped = strings.ReplaceAll(escaped, "<", "&lt;")
		escaped = strings.ReplaceAll(escaped, ">", "&gt;")
		text = strings.Replace(text, fmt.Sprintf("\x00CB%d\x00", i), "<pre><code>"+escaped+"</code></pre>", 1)
	}

	return text
}

// ---------------------------------------------------------------------------
// Typing indicator
// ---------------------------------------------------------------------------

func startTypingIndicator(ctx context.Context, b *tgbot.Bot, chatID int64, threadID int) context.CancelFunc {
	typingCtx, typingCancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		sendTyping(typingCtx, b, chatID, threadID)
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				sendTyping(typingCtx, b, chatID, threadID)
			}
		}
	}()
	return typingCancel
}

func sendTyping(ctx context.Context, b *tgbot.Bot, chatID int64, threadID int) {
	params := &tgbot.SendChatActionParams{
		ChatID: chatID,
		Action: models.ChatActionTyping,
	}
	if threadID != 0 {
		params.MessageThreadID = threadID
	}
	_, _ = b.SendChatAction(ctx, params)
}

// ---------------------------------------------------------------------------
// SendMessage - exported for heartbeat forwarding
// ---------------------------------------------------------------------------

// SendMessage sends a message to a specific chat. Exported for heartbeat forwarding.
// The token parameter is accepted for API compatibility but ignored; the module-level
// bot instance is used instead.
func SendMessage(_ string, chatID int64, text string) error {
	if botInstance == nil {
		return fmt.Errorf("telegram bot not initialized")
	}
	return sendMessageInternal(context.Background(), botInstance, chatID, text, 0)
}

func sendMessageInternal(ctx context.Context, b *tgbot.Bot, chatID int64, text string, threadID int) error {
	normalized := normalizeTelegramText(text)
	normalized = reactRe.ReplaceAllString(normalized, "")
	html := markdownToTelegramHTML(normalized)

	if html == "" {
		html = normalized
	}

	for i := 0; i < len(html); i += maxMsgLen {
		end := i + maxMsgLen
		if end > len(html) {
			end = len(html)
		}
		chunk := html[i:end]

		params := &tgbot.SendMessageParams{
			ChatID:    chatID,
			Text:      chunk,
			ParseMode: models.ParseModeHTML,
		}
		if threadID != 0 {
			params.MessageThreadID = threadID
		}

		_, err := b.SendMessage(ctx, params)
		if err != nil {
			// Fallback to plain text if HTML parsing fails
			plainStart := i
			if plainStart > len(normalized) {
				plainStart = len(normalized)
			}
			plainEnd := plainStart + maxMsgLen
			if plainEnd > len(normalized) {
				plainEnd = len(normalized)
			}
			plainChunk := normalized[plainStart:plainEnd]
			plainParams := &tgbot.SendMessageParams{
				ChatID: chatID,
				Text:   plainChunk,
			}
			if threadID != 0 {
				plainParams.MessageThreadID = threadID
			}
			if _, err2 := b.SendMessage(ctx, plainParams); err2 != nil {
				return fmt.Errorf("sendMessage fallback failed: %w", err2)
			}
		}
	}
	return nil
}

func sendReaction(ctx context.Context, b *tgbot.Bot, chatID int64, messageID int, emoji string) error {
	_, err := b.SetMessageReaction(ctx, &tgbot.SetMessageReactionParams{
		ChatID:    chatID,
		MessageID: messageID,
		Reaction: []models.ReactionType{
			{
				Type:              models.ReactionTypeTypeEmoji,
				ReactionTypeEmoji: &models.ReactionTypeEmoji{Emoji: emoji},
			},
		},
	})
	return err
}

// ---------------------------------------------------------------------------
// Group trigger detection
// ---------------------------------------------------------------------------

func groupTriggerReason(msg *models.Message) string {
	mu.Lock()
	currentBotID := botIDVal
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

	runes := []rune(text)
	for _, entity := range entities {
		if entity.Offset+entity.Length > len(runes) {
			continue
		}
		value := string(runes[entity.Offset : entity.Offset+entity.Length])

		if string(entity.Type) == "mention" && currentBotUsername != "" && strings.EqualFold(value, "@"+currentBotUsername) {
			return "mention_entity_matches_bot"
		}
		if string(entity.Type) == "mention" && currentBotUsername == "" {
			return "mention_entity_before_botname_loaded"
		}
		if string(entity.Type) == "bot_command" {
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

func downloadFileByURL(url string) ([]byte, error) {
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

func downloadImageFromMessage(ctx context.Context, b *tgbot.Bot, msg *models.Message) (string, error) {
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

	fileMeta, err := b.GetFile(ctx, &tgbot.GetFileParams{FileID: fileID})
	if err != nil {
		return "", fmt.Errorf("getFile: %w", err)
	}
	if fileMeta.FilePath == "" {
		return "", fmt.Errorf("getFile returned empty file_path")
	}

	downloadURL := b.FileDownloadLink(fileMeta)
	data, err := downloadFileByURL(downloadURL)
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

	filename := fmt.Sprintf("%d-%d-%d%s", msg.Chat.ID, msg.ID, time.Now().UnixMilli(), ext)
	localPath := filepath.Join(dir, filename)
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write image file: %w", err)
	}
	return localPath, nil
}

func downloadVoiceFromMessage(ctx context.Context, b *tgbot.Bot, msg *models.Message) (string, error) {
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

	fileMeta, err := b.GetFile(ctx, &tgbot.GetFileParams{FileID: fileID})
	if err != nil {
		return "", fmt.Errorf("getFile: %w", err)
	}
	if fileMeta.FilePath == "" {
		return "", fmt.Errorf("getFile returned empty file_path")
	}

	downloadURL := b.FileDownloadLink(fileMeta)
	data, err := downloadFileByURL(downloadURL)
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

	filename := fmt.Sprintf("%d-%d-%d%s", msg.Chat.ID, msg.ID, time.Now().UnixMilli(), ext)
	localPath := filepath.Join(dir, filename)
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write voice file: %w", err)
	}

	debugLog("Voice download: wrote %d bytes to %s ext=%s", len(data), localPath, ext)
	return localPath, nil
}

// ---------------------------------------------------------------------------
// Secretary reply helper
// ---------------------------------------------------------------------------

func trySecretaryReply(ctx context.Context, b *tgbot.Bot, chatID int64, threadID int, replyMsgID int, text string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	lookupURL := fmt.Sprintf("http://127.0.0.1:9999/pending/by-bot-msg/%d", replyMsgID)
	resp, err := client.Get(lookupURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	var item struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil || item.ID == "" {
		return false
	}

	confirmURL := fmt.Sprintf("http://127.0.0.1:9999/confirm/%s/custom", item.ID)
	bodyData, _ := json.Marshal(map[string]string{"text": text})
	confirmResp, err := client.Post(confirmURL, "application/json", bytes.NewReader(bodyData))
	if err != nil {
		return false
	}
	confirmResp.Body.Close()

	_ = sendMessageInternal(ctx, b, chatID, "Sent custom reply + pattern learned.", threadID)
	return true
}

// ---------------------------------------------------------------------------
// Callback query handler
// ---------------------------------------------------------------------------

var secretaryCallbackRe = regexp.MustCompile(`^sec_(yes|no)_([0-9a-f]{8})$`)

func handleCallbackQuery(ctx context.Context, b *tgbot.Bot, query *models.CallbackQuery) {
	data := query.Data

	// Secretary pattern: "sec_yes_<8hex>" or "sec_no_<8hex>"
	if matches := secretaryCallbackRe.FindStringSubmatch(data); matches != nil {
		action := matches[1]
		pendingID := matches[2]
		answerText := "Server error"

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

			// Try to edit the original message
			if query.Message.Type == models.MaybeInaccessibleMessageTypeMessage && query.Message.Message != nil {
				origMsg := query.Message.Message
				statusLine := "\n\nDismissed"
				if action == "yes" {
					statusLine = "\n\nSent"
				}
				oldText := origMsg.Text
				replyRe := regexp.MustCompile(`(?s)\n\nReply:.*$`)
				newText := replyRe.ReplaceAllString(oldText, statusLine)
				if newText == oldText {
					newText = oldText + statusLine
				}
				_, _ = b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
					ChatID:    origMsg.Chat.ID,
					MessageID: origMsg.ID,
					Text:      newText,
				})
			}
		}()

		_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            answerText,
		})
		return
	}

	// Default: ack with no text
	_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})
}

// ---------------------------------------------------------------------------
// chatMemberUser extracts the User from a ChatMember discriminated union.
// ---------------------------------------------------------------------------

func chatMemberUser(cm models.ChatMember) *models.User {
	switch cm.Type {
	case models.ChatMemberTypeOwner:
		if cm.Owner != nil {
			return cm.Owner.User
		}
	case models.ChatMemberTypeAdministrator:
		return &cm.Administrator.User
	case models.ChatMemberTypeMember:
		if cm.Member != nil {
			return cm.Member.User
		}
	case models.ChatMemberTypeRestricted:
		if cm.Restricted != nil {
			return cm.Restricted.User
		}
	case models.ChatMemberTypeLeft:
		if cm.Left != nil {
			return cm.Left.User
		}
	case models.ChatMemberTypeBanned:
		if cm.Banned != nil {
			return cm.Banned.User
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// my_chat_member handler
// ---------------------------------------------------------------------------

func handleMyChatMember(ctx context.Context, b *tgbot.Bot, update *models.ChatMemberUpdated) {
	chat := update.Chat

	newUser := chatMemberUser(update.NewChatMember)
	if newUser != nil {
		mu.Lock()
		if botUsername == "" && newUser.Username != "" {
			botUsername = newUser.Username
		}
		if botIDVal == 0 {
			botIDVal = newUser.ID
		}
		mu.Unlock()
	}

	oldStatus := update.OldChatMember.Type
	newStatus := update.NewChatMember.Type
	isGroup := chat.Type == models.ChatTypeGroup || chat.Type == models.ChatTypeSupergroup
	wasOut := oldStatus == models.ChatMemberTypeLeft || oldStatus == models.ChatMemberTypeBanned
	isIn := newStatus == models.ChatMemberTypeMember || newStatus == models.ChatMemberTypeAdministrator

	if !isGroup || !wasOut || !isIn {
		return
	}

	chatName := chat.Title
	if chatName == "" {
		chatName = fmt.Sprintf("%d", chat.ID)
	}
	log.Printf("[Telegram] Added to %s: %s (%d) by %d", string(chat.Type), chatName, chat.ID, update.From.ID)

	addedBy := update.From.Username
	if addedBy == "" {
		addedBy = fmt.Sprintf("%s (%d)", update.From.FirstName, update.From.ID)
	}

	eventPrompt := fmt.Sprintf(
		"[Telegram system event] I was added to a %s.\nGroup title: %s\nGroup id: %d\nAdded by: %s\nWrite a short first message for the group. It should confirm I was added and explain how to trigger me.",
		string(chat.Type), chatName, chat.ID, addedBy,
	)

	result, err := runner.Run(ctx, "telegram", eventPrompt)
	if err != nil || result.ExitCode != 0 {
		cfg := config.GetSettings()
		_ = SendMessage(cfg.Telegram.Token, chat.ID, "I was added to this group. Mention me with a command to start.")
		if err != nil {
			log.Printf("[Telegram] group-added event error: %v", err)
		}
		return
	}
	msg := result.Stdout
	if msg == "" {
		msg = "I was added to this group."
	}
	cfg := config.GetSettings()
	_ = SendMessage(cfg.Telegram.Token, chat.ID, msg)
}

// ---------------------------------------------------------------------------
// Message handler
// ---------------------------------------------------------------------------

func handleMessage(ctx context.Context, b *tgbot.Bot, msg *models.Message) {
	cfg := config.GetSettings()
	var userID int64
	if msg.From != nil {
		userID = msg.From.ID
	}
	chatID := msg.Chat.ID
	threadID := msg.MessageThreadID
	text, _ := getMessageTextAndEntities(msg)
	chatType := msg.Chat.Type
	isPrivate := chatType == models.ChatTypePrivate
	isGroup := chatType == models.ChatTypeGroup || chatType == models.ChatTypeSupergroup
	hasImage := len(msg.Photo) > 0 || isImageDocument(msg.Document)
	hasVoice := msg.Voice != nil || msg.Audio != nil || isAudioDocument(msg.Document)

	if !isPrivate && !isGroup {
		return
	}

	triggerReason := "private_chat"
	if isGroup {
		triggerReason = groupTriggerReason(msg)
		if triggerReason == "" {
			debugLog("Skip group message chat=%d from=%d reason=no_trigger text=%q", chatID, userID, truncate(text, 80))
			return
		}
	}
	debugLog("Handle message chat=%d type=%s from=%d reason=%s text=%q", chatID, string(chatType), userID, triggerReason, truncate(text, 80))

	// Authorization check
	if userID != 0 && len(cfg.Telegram.AllowedUserIds) > 0 {
		allowed := false
		for _, id := range cfg.Telegram.AllowedUserIds {
			if int64(id) == userID {
				allowed = true
				break
			}
		}
		if !allowed {
			if isPrivate {
				_ = sendMessageInternal(ctx, b, chatID, "Unauthorized.", threadID)
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
		_ = sendMessageInternal(ctx, b, chatID, "Hello! Send me a message and I'll respond using Claude.\nUse /reset to start a fresh session.", threadID)
		return
	}

	if command == "/reset" {
		if err := sessions.ResetSession(); err != nil {
			log.Printf("[Telegram] Reset session error: %v", err)
		}
		_ = sendMessageInternal(ctx, b, chatID, "Global session reset. Next message starts fresh.", threadID)
		return
	}

	// Secretary: detect reply to a bot alert message
	mu.Lock()
	currentBotID := botIDVal
	mu.Unlock()
	if msg.ReplyToMessage != nil && text != "" && currentBotID != 0 &&
		msg.ReplyToMessage.From != nil && msg.ReplyToMessage.From.ID == currentBotID {
		replyMsgID := msg.ReplyToMessage.ID
		if trySecretaryReply(ctx, b, chatID, threadID, replyMsgID, text) {
			return
		}
	}

	label := fmt.Sprintf("%d", userID)
	if msg.From != nil && msg.From.Username != "" {
		label = msg.From.Username
	}

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

	log.Printf("[%s] Telegram %s%s: %q", time.Now().Format("15:04:05"), label, mediaSuffix, truncate(text, 60))

	// Start typing indicator with context cancellation.
	typingCtx, typingCancel := context.WithCancel(ctx)
	defer typingCancel()
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		sendTyping(typingCtx, b, chatID, threadID)
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				sendTyping(typingCtx, b, chatID, threadID)
			}
		}
	}()

	// Download image if present
	var imagePath string
	if hasImage {
		path, err := downloadImageFromMessage(ctx, b, msg)
		if err != nil {
			log.Printf("[Telegram] Failed to download image for %s: %v", label, err)
		} else {
			imagePath = path
		}
	}

	// Download and transcribe voice if present
	var voiceTranscript string
	if hasVoice {
		voicePath, err := downloadVoiceFromMessage(ctx, b, msg)
		if err != nil {
			log.Printf("[Telegram] Failed to download voice for %s: %v", label, err)
		} else if voicePath != "" {
			debugLog("Voice file saved: path=%s", voicePath)
			transcript, err := whisper.TranscribeAudioToText(voicePath, debugMode)
			if err != nil {
				log.Printf("[Telegram] Failed to transcribe voice for %s: %v", label, err)
			} else {
				voiceTranscript = transcript
			}
		}
	}

	// Skill routing: resolve slash commands to SKILL.md prompts
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
	if threadID != 0 {
		promptParts = append(promptParts, fmt.Sprintf("[thread:%d]", threadID))
	}

	if skillContext != "" {
		args := strings.TrimSpace(text)
		if command != "" && len(args) >= len(command) {
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

	// Use context.Context with timeout for the runner call
	runCtx, runCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer runCancel()

	result, err := runner.RunUserMessage(runCtx, "telegram", prefixedPrompt)
	if err != nil {
		log.Printf("[Telegram] Error for %s: %v", label, err)
		_ = sendMessageInternal(ctx, b, chatID, fmt.Sprintf("Error: %v", err), threadID)
		return
	}

	if result.ExitCode != 0 {
		errText := result.Stderr
		if errText == "" {
			errText = "Unknown error"
		}
		_ = sendMessageInternal(ctx, b, chatID, fmt.Sprintf("Error (exit %d): %s", result.ExitCode, errText), threadID)
		return
	}

	stdout := result.Stdout
	if stdout == "" {
		stdout = "(empty response)"
	}
	cleanedText, reactionEmoji := extractReactionDirective(stdout)
	if reactionEmoji != "" {
		if err := sendReaction(ctx, b, chatID, msg.ID, reactionEmoji); err != nil {
			log.Printf("[Telegram] Failed to send reaction for %s: %v", label, err)
		}
	}
	if cleanedText == "" {
		cleanedText = "(empty response)"
	}
	_ = sendMessageInternal(ctx, b, chatID, cleanedText, threadID)
}

// ---------------------------------------------------------------------------
// Bot command registration
// ---------------------------------------------------------------------------

func registerBotCommands(ctx context.Context, b *tgbot.Bot) {
	allSkills := skills.ListSkills()

	commands := []models.BotCommand{
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
		commands = append(commands, models.BotCommand{Command: cmd, Description: desc})
	}

	if len(commands) > 100 {
		commands = commands[:100]
	}

	_, err := b.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{
		Commands: commands,
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
// Default update handler (dispatches all update types)
// ---------------------------------------------------------------------------

func defaultUpdateHandler(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	// Collect all incoming messages
	var incoming []*models.Message
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

	for _, m := range incoming {
		go func(msg *models.Message) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Telegram] handleMessage panic: %v", r)
				}
			}()
			handleMessage(ctx, b, msg)
		}(m)
	}

	if update.MyChatMember != nil {
		go func(u *models.ChatMemberUpdated) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Telegram] my_chat_member panic: %v", r)
				}
			}()
			handleMyChatMember(ctx, b, u)
		}(update.MyChatMember)
	}

	if update.CallbackQuery != nil {
		go func(q *models.CallbackQuery) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Telegram] callback_query panic: %v", r)
				}
			}()
			handleCallbackQuery(ctx, b, q)
		}(update.CallbackQuery)
	}
}

// ---------------------------------------------------------------------------
// Bot creation helper
// ---------------------------------------------------------------------------

func newBot(token string) (*tgbot.Bot, error) {
	opts := []tgbot.Option{
		tgbot.WithDefaultHandler(defaultUpdateHandler),
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{"message", "my_chat_member", "callback_query"}),
	}
	if debugMode {
		opts = append(opts, tgbot.WithDebugHandler(func(format string, args ...any) {
			log.Printf("[Telegram][debug] "+format, args...)
		}))
	}
	return tgbot.New(token, opts...)
}

// ---------------------------------------------------------------------------
// Exported entry points
// ---------------------------------------------------------------------------

// StartPolling starts the Telegram polling loop in a background goroutine.
func StartPolling(ctx context.Context, debug bool) {
	debugMode = debug

	go func() {
		runner.EnsureProjectClaudeMd()

		cfg := config.GetSettings()
		token := cfg.Telegram.Token

		b, err := newBot(token)
		if err != nil {
			log.Printf("[Telegram] Failed to create bot: %v", err)
			return
		}
		botInstance = b

		me, err := b.GetMe(ctx)
		if err != nil {
			log.Printf("[Telegram] getMe failed: %v", err)
		} else {
			mu.Lock()
			botUsername = me.Username
			botIDVal = me.ID
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
		if len(cfg.Telegram.AllowedUserIds) == 0 {
			log.Printf("  Allowed users: all")
		} else {
			parts := make([]string, len(cfg.Telegram.AllowedUserIds))
			for i, id := range cfg.Telegram.AllowedUserIds {
				parts[i] = fmt.Sprintf("%d", id)
			}
			log.Printf("  Allowed users: %s", strings.Join(parts, ", "))
		}
		if debugMode {
			log.Println("  Debug: enabled")
		}

		go registerBotCommands(ctx, b)

		b.Start(ctx)
	}()
}

// Telegram is the standalone entry point for running the Telegram bot.
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

	cfg := config.GetSettings()
	token := cfg.Telegram.Token

	b, err := newBot(token)
	if err != nil {
		log.Fatalf("[Telegram] Failed to create bot: %v", err)
	}
	botInstance = b

	me, err := b.GetMe(ctx)
	if err != nil {
		log.Printf("[Telegram] getMe failed: %v", err)
	} else {
		mu.Lock()
		botUsername = me.Username
		botIDVal = me.ID
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
	if len(cfg.Telegram.AllowedUserIds) == 0 {
		log.Printf("  Allowed users: all")
	} else {
		parts := make([]string, len(cfg.Telegram.AllowedUserIds))
		for i, id := range cfg.Telegram.AllowedUserIds {
			parts[i] = fmt.Sprintf("%d", id)
		}
		log.Printf("  Allowed users: %s", strings.Join(parts, ", "))
	}
	if debugMode {
		log.Println("  Debug: enabled")
	}

	go registerBotCommands(ctx, b)

	b.Start(ctx)
}
