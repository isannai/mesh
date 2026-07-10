package telegram

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// thinkingText is the placeholder shown while the AI is processing.
const thinkingText = "🤔 생각 중..."

// thinking is an in-progress "AI 생각 중" indicator. It shows a placeholder
// message plus the native "typing…" chat action, and is replaced by the real
// answer when finish() is called.
type thinking struct {
	api       *bot.Bot
	chatID    int64
	messageID int    // placeholder message id (0 if it could not be sent)
	isAnim    bool   // true when the placeholder is a GIF (delete) vs text (edit)
	stop      chan struct{}
}

// startThinking shows the indicator and returns a handle to finish it.
// If a thinking GIF is configured it is shown; otherwise a text placeholder.
func (b *Bot) startThinking(ctx context.Context, api *bot.Bot, chatID int64) *thinking {
	t := &thinking{api: api, chatID: chatID, stop: make(chan struct{})}

	// Native "...입력 중" animation. It expires after ~5s, so keepTyping refreshes it.
	api.SendChatAction(ctx, &bot.SendChatActionParams{
		ChatID: chatID,
		Action: models.ChatActionTyping,
	})

	switch {
	case b.thinkingSticker != "":
		// Sticker: keeps transparency, renders small. Deleted + replaced on finish.
		b.sendThinkingSticker(ctx, api, chatID, t)
	case b.thinkingAnim != "":
		// GIF (Telegram fills transparency). Deleted + replaced on finish.
		b.sendThinkingAnimation(ctx, api, chatID, t)
	default:
		// Text placeholder: edited in place into the answer on finish (no flicker).
		if msg, err := api.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   thinkingText,
		}); err == nil {
			t.messageID = msg.ID
		}
	}

	go t.keepTyping(ctx)
	return t
}

// sendThinkingSticker shows the configured sticker (custom 캐릭터). Like the GIF
// path, the first send may upload a local .webm/.tgs/.webp or fetch a URL, and
// the returned file_id is cached for reuse. On failure it falls back to text.
func (b *Bot) sendThinkingSticker(ctx context.Context, api *bot.Bot, chatID int64, t *thinking) {
	input, closer := b.resolveMediaInput(b.thinkingSticker)
	if closer != nil {
		defer closer.Close()
	}

	msg, err := api.SendSticker(ctx, &bot.SendStickerParams{
		ChatID:  chatID,
		Sticker: input,
	})
	if err != nil {
		if m, e := api.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: thinkingText}); e == nil {
			t.messageID = m.ID
		}
		return
	}

	t.messageID = msg.ID
	t.isAnim = true // media -> delete + send a fresh answer on finish (no edit)
	if msg.Sticker != nil && msg.Sticker.FileID != "" {
		b.cacheThinkingFileID(msg.Sticker.FileID)
	}
}

// sendThinkingAnimation shows the configured GIF and records the placeholder in
// t. The first send may upload a local file or have Telegram fetch a URL; the
// returned file_id is cached so subsequent sends transmit only the id (no
// re-upload / re-fetch). On failure it falls back to a text placeholder.
func (b *Bot) sendThinkingAnimation(ctx context.Context, api *bot.Bot, chatID int64, t *thinking) {
	input, closer := b.resolveMediaInput(b.thinkingAnim)
	if closer != nil {
		defer closer.Close()
	}

	msg, err := api.SendAnimation(ctx, &bot.SendAnimationParams{
		ChatID:    chatID,
		Animation: input,
		Caption:   thinkingText,
	})
	if err != nil {
		// Bad path/URL/file_id -> degrade to a text placeholder so the user still
		// sees a "생각 중" indicator.
		if m, e := api.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: thinkingText}); e == nil {
			t.messageID = m.ID
		}
		return
	}

	t.messageID = msg.ID
	t.isAnim = true
	if msg.Animation != nil && msg.Animation.FileID != "" {
		b.cacheThinkingFileID(msg.Animation.FileID)
	}
}

// resolveMediaInput resolves a configured media value (GIF or sticker) into an
// InputFile:
//   - a previously cached file_id (preferred: nothing is transferred)
//   - an http(s) URL (Telegram fetches it)
//   - an existing local file (uploaded; caller MUST close the returned io.Closer)
//   - otherwise treated as a Telegram file_id
func (b *Bot) resolveMediaInput(value string) (models.InputFile, io.Closer) {
	if id := b.cachedThinkingFileID(); id != "" {
		return &models.InputFileString{Data: id}, nil
	}

	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return &models.InputFileString{Data: value}, nil
	}
	if f, err := os.Open(value); err == nil {
		return &models.InputFileUpload{Filename: filepath.Base(value), Data: f}, f
	}
	return &models.InputFileString{Data: value}, nil
}

func (b *Bot) cachedThinkingFileID() string {
	b.thinkingMu.Lock()
	defer b.thinkingMu.Unlock()
	return b.thinkingFileID
}

func (b *Bot) cacheThinkingFileID(id string) {
	b.thinkingMu.Lock()
	defer b.thinkingMu.Unlock()
	b.thinkingFileID = id
}

// keepTyping refreshes the typing action every 4s until finished or cancelled.
func (t *thinking) keepTyping(ctx context.Context) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.api.SendChatAction(ctx, &bot.SendChatActionParams{
				ChatID: t.chatID,
				Action: models.ChatActionTyping,
			})
		}
	}
}

// finish stops the indicator and shows text in place of the placeholder.
func (t *thinking) finish(ctx context.Context, text string) {
	close(t.stop)

	// Text placeholder -> edit in place (smoothest, keeps message order).
	if t.messageID != 0 && !t.isAnim {
		if _, err := t.api.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    t.chatID,
			MessageID: t.messageID,
			Text:      text,
		}); err == nil {
			return
		}
	}

	// Sticker/GIF placeholder (or edit failed): send the answer FIRST so the text
	// is already on screen, THEN remove the sticker — this avoids the empty gap
	// that a delete-then-send order produces (image vanishes, brief pause, text).
	t.api.SendMessage(ctx, &bot.SendMessageParams{ChatID: t.chatID, Text: text})
	if t.messageID != 0 {
		t.api.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    t.chatID,
			MessageID: t.messageID,
		})
	}
}
