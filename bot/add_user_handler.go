package bot

import (
	"context"
	"strconv"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (ub *UndercastBot) addUserHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	chatID := ub.extractChatID(update)

	isAdmin, err := ub.auth.IsAdmin(ctx, ub.extractUsername(update))
	if err != nil {
		ub.handleError(ctx, chatID, err)
		return
	}

	if !isAdmin {
		ub.sendTextMessage(ctx, chatID, "unknown command")
		return
	}

	if update.Message == nil || update.Message.Contact == nil {
		ub.sendTextMessage(ctx, chatID, "please share a contact")
		return
	}

	userIDToAdd := strconv.FormatInt(update.Message.Contact.UserID, 10)
	if err := ub.auth.AddUser(ctx, userIDToAdd); err != nil {
		ub.handleError(ctx, chatID, err)
		return
	}

	ub.sendTextMessage(ctx, chatID, "user added")
}
