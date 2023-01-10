package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/hori-ryota/zaperr"
	"go.uber.org/zap"
	"undercast-bot/service"
)

func (ub *UndercastBot) feedsHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := ub.extractUsername(update)
	zapFields := []zap.Field{
		zap.Int64("chatID", update.Message.Chat.ID),
		zap.String("messageText", update.Message.Text),
		zap.String("userID", userID),
	}

	feeds, err := ub.service.ListFeeds(ctx, userID)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to list feeds", zapFields...))
		return
	}

	episodes, err := ub.service.ListEpisodes(ctx, userID)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to list episodes", zapFields...))
		return
	}

	episodesMap := map[string]*service.Episode{}
	for _, ep := range episodes {
		episodesMap[ep.ID] = ep
	}

	for _, f := range feeds {
		if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    update.Message.Chat.ID,
			Text:      ub.renderFeed(f, episodesMap),
			ParseMode: models.ParseModeHTML,
		}); err != nil {
			ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to send message", zapFields...))
		}
	}

}

func (ub *UndercastBot) renderFeed(f *service.Feed, episodesMap map[string]*service.Episode) string {
	var renderedEpisodesBits []string
	for _, id := range f.EpisodeIDs {
		ep := episodesMap[id]
		if ep == nil {
			continue
		}
		renderedEpisodesBits = append(renderedEpisodesBits, fmt.Sprintf("Episode #<code>%s</code>: <b>%s</b>", ep.ID, ep.Title))
	}
	renderedEpisodes := strings.Join(renderedEpisodesBits, "\n")

	return fmt.Sprintf(`Feed #<code>%s</code>
<b>%s</b>

Episodes (%d):
%s`, f.ID, f.Title, len(f.EpisodeIDs), renderedEpisodes)
}
