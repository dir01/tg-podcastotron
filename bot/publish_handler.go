package bot

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/hori-ryota/zaperr"
	"go.uber.org/zap"
	"undercast-bot/service"
)

const publishHelp = `
*Publish episodes to feed*:

/publish_ep_<episode_id>_in_<feed_id>

or for multiple episodes

/publish_ep_<episode_id1>_<episode_id2>_in_<feed_id>

or for range of episodes

/publish_ep_<episode1_id>_to_<episode10_id>_in_<feed_id>
`

func (ub *UndercastBot) publishEpisodesHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	zapFields := []zap.Field{
		zap.Int("chatID", update.Message.Chat.ID),
		zap.String("messageText", update.Message.Text),
	}

	epIDs, feedID, err := ub.parsePublishEpisodesCommand(update.Message.Text)
	if err != nil {
		ub.sendTextMessage(ctx, update.Message.Chat.ID, publishHelp)
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to parse publish command", zapFields...))
		return
	}

	zapFields = append(zapFields, zap.Strings("episodeIDs", epIDs), zap.String("feedID", feedID))

	if err := ub.service.PublishEpisodes(ctx, epIDs, feedID, ub.extractUsername(update)); err != nil {
		if errors.Is(err, service.ErrFeedNotFound) {
			ub.sendTextMessage(ctx, update.Message.Chat.ID, publishHelp+"\n\nFeed not found")
		} else {
			ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to publish episodes", zapFields...))
		}
		return
	}

	subject := "Episode"
	if len(epIDs) > 1 {
		subject = "Episodes"
	}
	ub.sendTextMessage(ctx, update.Message.Chat.ID, "%s %s were published to feed %s", subject, strings.Join(epIDs, ", "), feedID)
}

func (ub *UndercastBot) parsePublishEpisodesCommand(text string) (epIDs []string, feedID string, err error) {
	re, err := regexp.Compile(`/publish_ep_(.*)_in_(\d+)`)
	if err != nil {
		return nil, "", fmt.Errorf("failed to compile regexp: %w", err)
	}

	matches := re.FindStringSubmatch(text)
	if len(matches) != 3 {
		return nil, "", fmt.Errorf(`failed to extract episode ids and feed id from the message '%s'`, text)
	}

	epIDs, err = parseIDs(matches[1])
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse episode ids: %w", err)
	}

	feedID = matches[2]
	return epIDs, feedID, nil
}
