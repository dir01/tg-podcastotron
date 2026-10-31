package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.opentelemetry.io/otel/attribute"
	"tg-podcastotron/telemetry"
)

// trace starts a span for each update. The span name is fixed, because the
// message text and the callback data can be any value (for example, a full
// magnet URI). The text and the data go into attributes.
func (ub *UndercastBot) trace(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		spanName := "telegram.update"
		var attrs []attribute.KeyValue
		if update.Message != nil && update.Message.Text != "" {
			spanName = "telegram.message"
			attrs = append(attrs, attribute.String("telegram.message.text", update.Message.Text))
		} else if update.CallbackQuery != nil {
			spanName = "telegram.callback"
			attrs = append(attrs, attribute.String("telegram.callback.data", update.CallbackQuery.Data))
		}

		ctx, span := telemetry.StartSpan(ctx, spanName, attrs...)
		defer span.End()

		next(ctx, b, update)
	}
}
