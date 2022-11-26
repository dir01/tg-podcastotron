package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

func (ub *UndercastBot) playgroundHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text: fmt.Sprintf(
			`To unpublish them from default feed, send command

/unpublish_episodes_329c2a33a43e5f923d4771a47e7f09c5

To publish them to another feed, send command

/publish_episodes_22_to_33`),
	}); err != nil {
		ub.logger.Error("failed to send message",
			zap.Error(err),
		)
	}
}
