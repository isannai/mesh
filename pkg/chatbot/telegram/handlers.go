package telegram

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/isannai/mesh/pkg/chatbot/ai"
)

const helpText = `사용 가능한 명령어:
/start - 봇 시작 및 소개
/help  - 도움말 보기

명령어가 아닌 일반 메시지는 그대로 다시 보내드립니다(에코).`

// startHandler greets the user.
func (b *Bot) startHandler(ctx context.Context, api *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	b.reply(ctx, api, update.Message.Chat.ID,
		"안녕하세요! 메시지를 보내면 그대로 돌려드려요. /help 로 명령어를 확인하세요.")
}

// helpHandler lists the available commands.
func (b *Bot) helpHandler(ctx context.Context, api *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	b.reply(ctx, api, update.Message.Chat.ID, helpText)
}

// chatHandler is the default handler for plain text: it shows a "생각 중"
// indicator, asks the AI client for a reply, then replaces the indicator with
// the answer. (M1 stub echoes the prompt after a short simulated delay.)
func (b *Bot) chatHandler(ctx context.Context, api *bot.Bot, update *models.Update) {
	// Updates also arrive for edited messages, callbacks, joins, photos, etc.
	if update.Message == nil {
		return
	}
	chatID := update.Message.Chat.ID

	// 설정 도우미: 스티커를 보내면 그 file_id 를 알려준다. 이 값을 config 의
	// thinking_sticker 에 넣으면 "생각 중" 표시로 쓸 수 있다.
	if s := update.Message.Sticker; s != nil {
		b.reply(ctx, api, chatID, "🔖 sticker file_id:\n"+s.FileID)
		return
	}

	if update.Message.Text == "" {
		return
	}

	th := b.startThinking(ctx, api, chatID)

	resp, err := b.ai.Chat(ctx, ai.ChatRequest{Prompt: update.Message.Text})
	if err != nil {
		th.finish(ctx, "⚠️ 처리 중 오류가 발생했습니다: "+err.Error())
		return
	}
	th.finish(ctx, resp.Text)
}

// reply sends a text message to chatID.
func (b *Bot) reply(ctx context.Context, api *bot.Bot, chatID int64, text string) {
	api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
}
