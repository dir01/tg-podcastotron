package bot

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (ub *UndercastBot) newFeedHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	chatID := ub.extractChatID(update)
	if feedNamePromptMsg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        "Please enter a name for your new feed",
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: &models.ForceReply{ForceReply: true},
	}); err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to send message: %w", err))
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
					ub.handleError(ctx, chatID, fmt.Errorf("failed to create feed: %w", err))
					return
				}

				if _, err = ub.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: feedNamePromptMsg.ID}); err != nil {
					ub.logger.ErrorContext(ctx, "failed to delete feed name prompt message", slog.Any("error", err))
				}

				statusMsg := fmt.Sprintf("Feed was created:\n\n%s", ub.renderFeedShort(feed))

				if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
					ChatID:    chatID,
					Text:      statusMsg,
					ParseMode: models.ParseModeHTML,
				}); err != nil {
					ub.handleError(ctx, chatID, fmt.Errorf("failed to send message: %w", err))
					return
				}
			})
	}

}
