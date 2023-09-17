package bot

import (
	"context"
	"strconv"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (ub *UndercastBot) authenticate(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		chatID := ub.extractChatID(update)
		userID := ub.extractUserID(update)
		username := ub.extractUsername(update)
		if username == "" || userID == "" || chatID == 0 {
			return
		}

		_ = ub.repository.SetChatID(ctx, userID, chatID)

		if isAuthenticated, err := ub.auth.IsAuthenticated(ctx, userID, username); isAuthenticated && err == nil {
			next(ctx, b, update)
			return
		}

		ub.sendTextMessage(ctx, chatID, "You are not authorized to use this bot")
	}
}

func (ub *UndercastBot) extractChatID(update *models.Update) int64 {
	switch {
	case update.Message != nil:
		return update.Message.Chat.ID
	case update.CallbackQuery != nil:
		return update.CallbackQuery.Message.Chat.ID
	default:
		return 0
	}
}

func (ub *UndercastBot) extractUsername(update *models.Update) string {
	switch {
	case update.Message != nil:
		return update.Message.From.Username
	case update.CallbackQuery != nil:
		return update.CallbackQuery.Sender.Username
	default:
		return ""
	}
}

func (ub *UndercastBot) extractUserID(update *models.Update) string {
	switch {
	case update.Message != nil:
		return strconv.FormatInt(update.Message.From.ID, 10)
	case update.CallbackQuery != nil:
		return strconv.FormatInt(update.CallbackQuery.Sender.ID, 10)
	default:
		return ""
	}
}
