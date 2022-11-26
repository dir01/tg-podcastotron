package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
	"undercast-bot/service"
)

func (ub *UndercastBot) episodesHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	username := ub.extractUsername(update)
	if username == "" {
		return
	}

	episodes, err := ub.service.ListEpisodes(ctx, username)
	if err != nil {
		ub.logger.Error("episodesHandler error", zap.Error(err))
		return
	}

	if len(episodes) == 0 {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "You have no episodes yet",
		}); err != nil {
			ub.logger.Error("episodesHandler error", zap.Error(err))
		}
		return
	}

	for _, ep := range episodes {
		ub.renderEpisode(ctx, b, update, ep)
	}
}

func (ub *UndercastBot) renderEpisode(ctx context.Context, b *bot.Bot, update *models.Update, ep *service.Episode) {

	if msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text: fmt.Sprintf(`Episode %s (%s)

Please select `, ep.Title, strings.Join(ep.SourceFilepaths, "\n")),
	}); err != nil {
		ub.logger.Error("episodesHandler error", zap.Error(err), zap.Any("msg", msg))
	}
}
