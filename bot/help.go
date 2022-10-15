package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const helpMessage = `
Send a magnet link to start downloading.
You may be prompted to select specific files.

Send /start or /help to see this message again.
`

func (ub *UndercastBot) helpHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	ub.sendTextMessage(ctx, update.Message.Chat.ID, helpMessage)
}
