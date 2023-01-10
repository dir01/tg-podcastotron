package bot

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/hori-ryota/zaperr"
	"go.uber.org/zap"
)

const unpublishHelp = `
*Remove episodes to feed*:
(please note that episodes are not deleted)

/unpublish_ep_<episode_id>_from_<feed_id>

or for multiple episodes

/unpublish_ep_<episode_id1>_<episode_id2>_from_<feed_id>

or for range of episodes

/unpublish_ep_<episode1_id>_to_<episode10_id>_from_<feed_id>
`

func (ub *UndercastBot) unpublishEpisodesHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	zapFields := []zap.Field{
		zap.Int64("chatID", update.Message.Chat.ID),
		zap.String("messageText", update.Message.Text),
	}

	epIDs, feedID, err := ub.parseUnpublishEpisodesCommand(update.Message.Text)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to parse unpublish command", zapFields...))
		ub.sendTextMessage(ctx, update.Message.Chat.ID, unpublishHelp)
		return
	}

	zapFields = append(zapFields, zap.Strings("episodeIDs", epIDs), zap.String("feedID", feedID))

	if err := ub.service.UnpublishEpisodes(ctx, epIDs, feedID, ub.extractUsername(update)); err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to unpublish episodes", zapFields...))
		return
	}

	subject := "Episode"
	if len(epIDs) > 1 {
		subject = "Episodes"
	}
	ub.sendTextMessage(ctx, update.Message.Chat.ID, "%s %s were unpublished from feed %s", subject, strings.Join(epIDs, ", "), feedID)
}

func (ub *UndercastBot) parseUnpublishEpisodesCommand(text string) (epIDs []string, feedID string, err error) {
	re, err := regexp.Compile(`/unpublish_ep_(.*)_from_(\d+)`)
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
