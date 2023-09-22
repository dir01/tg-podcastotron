package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/hori-ryota/zaperr"
	"go.uber.org/zap"
)

func (ub *UndercastBot) newFeedHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID := ub.extractChatID(update)
	zapFields := []zap.Field{
		zap.Int64("chat_id", chatID),
	}
	if feedNamePromptMsg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        "Please enter a name for your new feed",
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: &models.ForceReply{ForceReply: true},
	}); err != nil {
		zapFields = append(zapFields, zap.Any("message", feedNamePromptMsg))
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zapFields...))
		return
	} else {
		ub.bot.RegisterHandlerMatchFunc(
			func(update *models.Update) bool {
				return update.Message.ReplyToMessage != nil && update.Message.ReplyToMessage.ID == feedNamePromptMsg.ID
			},
			func(ctx context.Context, b *bot.Bot, update *models.Update) {
				feedTitle := update.Message.Text
				userID := ub.extractUserID(update)
				feed, err := ub.service.CreateFeed(ctx, userID, feedTitle)
				if err != nil {
					zapFields := append(zapFields, zap.String("feed_title", feedTitle))
					ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to create feed", zapFields...))
					return
				}

				if _, err = ub.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: feedNamePromptMsg.ID}); err != nil {
					zapFields := append(zapFields, zaperr.ToField(err))
					ub.logger.Error("failed to delete feed name prompt message", zapFields...)
				}

				statusMsg := fmt.Sprintf("Feed was created:\n\n%s", ub.renderFeedShort(feed))

				if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
					ChatID:    chatID,
					Text:      statusMsg,
					ParseMode: models.ParseModeHTML,
				}); err != nil {
					zFields := append(zapFields, zap.String("message", statusMsg))
					ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zFields...))
					return
				}
			})
	}

}
