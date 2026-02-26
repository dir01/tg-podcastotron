package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tg-podcastotron/telemetry"
)

func (ub *UndercastBot) trace(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		spanName := "update"
		if update.Message != nil && update.Message.Text != "" {
			spanName = update.Message.Text
		} else if update.CallbackQuery != nil {
			spanName = "callback:" + update.CallbackQuery.Data
		}

		ctx, span := telemetry.StartSpan(ctx, spanName)
		defer span.End()

		next(ctx, b, update)
	}
}
