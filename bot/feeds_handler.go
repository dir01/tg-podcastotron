package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

func (ub *UndercastBot) feedsHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	feeds, err := ub.service.ListFeeds(ctx, ub.extractUsername(update))
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, err)
	}

	for _, f := range feeds {
		if msg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    update.Message.Chat.ID,
			Text:      fmt.Sprintf("<b>ID</b>: <code>%s</code>\n<b>Title</b>: %s\n\n<b>URL</b>: %s", f.ID, f.Title, f.URL),
			ParseMode: models.ParseModeHTML,
		}); err != nil {
			ub.logger.Error("feedsHandler error", zap.Error(err), zap.Any("msg", msg))
		}
	}

}
