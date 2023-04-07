package bot

import (
	"context"
	"fmt"
	"github.com/hori-ryota/zaperr"
	"net/url"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var sentMenusCache = make(map[string]bool) // TODO: cache invalidation

func (ub *UndercastBot) setMenuMiddleware(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		username := ub.extractUsername(update)
		if username == "" {
			next(ctx, b, update)
			return
		}

		commands := []models.BotCommand{
			{Command: "help", Description: "Display bot help"},
			{Command: "ep", Description: "List all your episodes"},
			{Command: "f", Description: "List all your podcast feeds"},
			{Command: "ee", Description: "Edit episode(s)"},
			{Command: "ef", Description: "Edit feed(s)"},
			{Command: "nf", Description: "Create new podcast feed"},
		}

		isAdmin, err := ub.auth.IsAdmin(ctx, username)
		if isAdmin && err == nil {
			commands = append(commands, models.BotCommand{
				Command:     "adduser",
				Description: "Invite a friend to use the system",
			})
		}

		params := url.Values{}
		params.Add("username", username)
		params.Add("isAdmin", fmt.Sprintf("%t", isAdmin))
		cacheKey := params.Encode()

		if !sentMenusCache[cacheKey] {
			if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: commands}); err != nil {
				ub.logger.Error("setMenuMiddleware error", zaperr.ToField(err))
			}
			sentMenusCache[cacheKey] = true
		}
		next(ctx, b, update)
	}
}
