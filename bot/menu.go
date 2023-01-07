package bot

import (
	"context"
	"fmt"
	"net/url"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

var sentMenusCache = make(map[string]bool) // TODO: cache invalidation

func (ub *UndercastBot) setMenuMiddleware(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		commands := []models.BotCommand{
			{"help", "Display bot help"},
			{"episodes", "List all your episodes"},
			{"feeds", "List all your podcast feeds"},
			//{"addFeed", "Create a new podcast feed"},
		}

		username := ub.extractUsername(update)
		if username == "" {
			next(ctx, b, update)
			return
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
				ub.logger.Error("setMenuMiddleware error", zap.Error(err))
			}
			sentMenusCache[cacheKey] = true
		}
		next(ctx, b, update)
	}
}
