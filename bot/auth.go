package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (ub *UndercastBot) authenticate(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		username := ub.extractUsername(update)
		if username == "" {
			return
		}

		chatID := ub.extractChatID(update, username)
		if chatID == 0 {
			return
		}

		ub.store.SetChatID(ctx, username, chatID)

		if isAuthenticated, err := ub.auth.IsAuthenticated(ctx, username); isAuthenticated && err == nil {
			next(ctx, b, update)
			return
		}

		ub.sendTextMessage(ctx, chatID, "You are not authorized to use this bot")
	}
}

func (ub *UndercastBot) extractChatID(update *models.Update, username string) int {
	if update.Message != nil {
		return update.Message.Chat.ID
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.Message.Chat.ID
	}
	return 0
}

func (ub *UndercastBot) extractUsername(update *models.Update) string {
	if update.Message != nil {
		return update.Message.From.Username
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.Sender.Username
	}
	return ""
}
