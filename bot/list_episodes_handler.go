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

func (ub *UndercastBot) listEpisodesHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := ub.extractUsername(update)
	if userID == "" {
		return
	}

	zapFields := []zap.Field{
		zap.Int64("chatID", update.Message.Chat.ID),
		zap.String("messageText", update.Message.Text),
		zap.String("userID", userID),
	}

	episodes, err := ub.service.ListEpisodes(ctx, userID)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to list episodes", zapFields...))
		return
	}

	if len(episodes) == 0 {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "You have no episodes yet",
		}); err != nil {
			ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to send message", zapFields...))
		}
		return
	}

	feeds, err := ub.service.ListFeeds(ctx, userID)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to list feeds", zapFields...))
		return
	}

	feedMap := map[string]*service.Feed{}
	for _, f := range feeds {
		feedMap[f.ID] = f
	}

	for _, ep := range episodes {
		text := ub.renderEpisodeFull(ep, feedMap)
		if msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    update.Message.Chat.ID,
			ParseMode: models.ParseModeHTML,
			Text:      text,
		}); err != nil {
			ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to send message", zap.Any("message", msg)))
			return
		}
	}
}

func (ub *UndercastBot) renderEpisodeFull(ep *service.Episode, feedMap map[string]*service.Feed) string {
	var feedsDescriptionBits []string
	for _, f := range ep.FeedIDs {
		feedsDescriptionBits = append(feedsDescriptionBits, fmt.Sprintf("- %s (%s)", feedMap[f].ID, feedMap[f].Title))
	}
	feedsDescription := strings.Join(feedsDescriptionBits, "\n")

	return fmt.Sprintf(`<b>Episode #<code>%s</code> (%s)</b>

<b>Source:</b>
<code>%s</code>

<b>Files:</b>
<code>%s</code>

Published to feeds:
%s`,
		ep.ID,
		ep.Title,
		ep.SourceURL,
		strings.Join(ep.SourceFilepaths, ", "),
		feedsDescription,
	)
}

func (ub *UndercastBot) renderEpisodeShort(ep *service.Episode) string {
	return fmt.Sprintf(`<b>Episode #<code>%s</code> (%s)</b>`, ep.ID, ep.Title)
}
