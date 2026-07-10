// Package telegram wires the Telegram long-polling bot: it constructs the
// underlying client, registers command/echo handlers, and runs the update loop.
//
// It depends on ai.Client (the seam) but never on the AI transport or crypto.
package telegram

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-telegram/bot"

	"github.com/isannai/mesh/pkg/chatbot/ai"
	"github.com/isannai/mesh/pkg/chatbot/config"
)

// Bot is the chatbot: a Telegram client plus the AI seam it delegates to.
type Bot struct {
	api *bot.Bot
	ai  ai.Client // plain text is routed here, wrapped with a "thinking" indicator

	// thinkingAnim / thinkingSticker are the configured "thinking" placeholders
	// (URL, local path, or Telegram file_id). A sticker takes precedence and
	// keeps transparency; empty both -> a text placeholder is used.
	thinkingAnim    string
	thinkingSticker string
	// thinkingFileID caches the file_id Telegram returns after the first send, so
	// later sends transmit only the id (no re-upload of a local file / re-fetch).
	thinkingMu     sync.Mutex
	thinkingFileID string
}

// New constructs the bot, registers handlers, and validates the token.
//
// bot.New performs a getMe call (~5s) so a bad/missing token fails fast here.
func New(cfg *config.Config, aiClient ai.Client) (*Bot, error) {
	b := &Bot{
		ai:              aiClient,
		thinkingAnim:    cfg.Telegram.ThinkingAnimation,
		thinkingSticker: cfg.Telegram.ThinkingSticker,
	}

	opts := []bot.Option{
		// Catch-all: plain text -> AI (with thinking indicator).
		bot.WithDefaultHandler(b.chatHandler),
	}

	api, err := bot.New(cfg.Telegram.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}
	b.api = api

	// Patterns are registered WITHOUT the leading slash: MatchTypeCommand
	// compares against the command name only (e.g. "/start" -> "start").
	// It also matches the group form "/start@YourBotName".
	// Specific handlers take precedence over the default echo handler.
	api.RegisterHandler(bot.HandlerTypeMessageText, "start", bot.MatchTypeCommand, b.startHandler)
	api.RegisterHandler(bot.HandlerTypeMessageText, "help", bot.MatchTypeCommand, b.helpHandler)

	return b, nil
}

// Run starts long-polling and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	b.api.Start(ctx)
}
