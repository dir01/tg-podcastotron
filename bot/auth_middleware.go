package bot

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.opentelemetry.io/otel/trace"
)

const noAccessMessage = "You have no access yet. Ask the admin for an invite link."

// authenticate lets only users with access through. /start does its own check,
// because it accepts invites from users without access.
func (ub *UndercastBot) authenticate(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if cq := update.CallbackQuery; cq != nil {
			// A button of the old chat UI.
			if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
				CallbackQueryID: cq.ID,
				Text:            "This button does not work now. Open the app.",
				ShowAlert:       true,
			}); err != nil {
				ub.logger.WarnContext(ctx, "failed to answer callback query", slog.Any("error", err))
			}
			return
		}
		msg := update.Message
		if msg == nil || msg.From == nil || msg.Chat.Type != models.ChatTypePrivate {
			return
		}
		userID := strconv.FormatInt(msg.From.ID, 10)
		if err := ub.repository.SetChatID(ctx, userID, msg.Chat.ID); err != nil {
			ub.logger.WarnContext(ctx, "failed to save chat id", slog.Any("error", err))
		}

		if isStartCommand(msg.Text) {
			next(ctx, b, update)
			return
		}
		if ok, err := ub.auth.IsAuthenticated(ctx, userID, msg.From.Username); err != nil {
			ub.handleError(ctx, msg.Chat.ID, err)
			return
		} else if !ok {
			ub.send(ctx, msg.Chat.ID, noAccessMessage, nil)
			return
		}
		next(ctx, b, update)
	}
}

func isStartCommand(text string) bool {
	return text == "/start" || strings.HasPrefix(text, "/start ")
}

func spanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}
