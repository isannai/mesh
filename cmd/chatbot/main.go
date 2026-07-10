// Command chatbot is the entrypoint for the Telegram chatbot.
//
// It loads configuration, wires the AI client into the Telegram bot, and runs
// the long-polling loop until interrupted (Ctrl+C).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/isannai/mesh/pkg/chatbot/ai"
	"github.com/isannai/mesh/pkg/chatbot/config"
	"github.com/isannai/mesh/pkg/chatbot/telegram"
)

func main() {
	configPath := flag.String("config", "./conf/config.json", "path to the JSON config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Real AI client (auth not wired yet). For offline testing, swap in ai.NopClient{}.
	// engine/preset/max_turns/node_id come from config; stream is fixed (one-shot).
	aiClient := ai.NewHTTPClient(ai.HTTPOptions{
		Endpoint:   cfg.AI.Endpoint,
		Engine:     cfg.AI.Engine,
		Preset:     cfg.AI.Preset,
		Nodes:      []string{cfg.AI.NodeID},
		MaxTurns:   cfg.AI.MaxTurns,
		Stream:     false,
		Timeout:    cfg.AI.TimeoutDuration(),
		Credential: cfg.AI.Credential,
	})

	b, err := telegram.New(cfg, aiClient)
	if err != nil {
		log.Fatalf("telegram: %v", err)
	}

	// Ctrl+C / SIGINT cancels ctx -> graceful shutdown. Works on win/linux/mac.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.Println("chatbot started; press Ctrl+C to stop")
	b.Run(ctx)
	log.Println("shutting down")
}
