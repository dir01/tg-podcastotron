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

func (ub *UndercastBot) listEpisodesHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := ub.extractUserID(update)
	chatID := ub.extractChatID(update)
	if userID == "" || chatID == 0 {
		return
	}

	epID := ub.parseListEpisodesCmd(update.Message.Text)

	zapFields := []zap.Field{
		zap.Int64("chatID", chatID),
		zap.String("messageText", update.Message.Text),
		zap.String("userID", userID),
		zap.String("username", ub.extractUsername(update)),
		zap.String("episodeID", epID),
	}

	var err error
	var episodes []*service.Episode
	feedMap := map[string]*service.Feed{}
	if epID == "" {
		if episodes, err = ub.service.ListEpisodes(ctx, userID); err != nil {
			ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to list episodes", zapFields...))
			return
		}
	} else {
		if epMap, err := ub.service.GetEpisodesMap(ctx, []string{epID}, userID); err != nil {
			if errors.Is(err, service.ErrEpisodeNotFound) {
				ub.sendTextMessage(ctx, chatID, "Episode %s not found", epID)
				return
			}
			ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to get episodes", zapFields...))
			return
		} else {
			episodes = append(episodes, epMap[epID])
			if feeds, err := ub.service.ListFeeds(ctx, userID); err != nil {
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to list feeds", zapFields...))
				return
			} else {
				for _, f := range feeds {
					feedMap[f.ID] = f
				}
			}
		}
	}

	if len(episodes) == 0 {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "You have no episodes yet",
		}); err != nil {
			ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zapFields...))
		}
		return
	}

	for _, ep := range episodes {
		var text string
		if epID == "" {
			text = ub.renderEpisodeShort(ep)
		} else {
			text = ub.renderEpisodeFull(ep, feedMap)
		}
		if msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			ParseMode: models.ParseModeHTML,
			Text:      text,
		}); err != nil {
			ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zap.Any("message", msg)))
			return
		}
	}
}

func (ub *UndercastBot) parseListEpisodesCmd(text string) (epID string) {
	re := regexp.MustCompile(`/ep_(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

func (ub *UndercastBot) renderEpisodeFull(ep *service.Episode, feedMap map[string]*service.Feed) string {
	var feedsDescriptionBits []string
	for _, f := range ep.FeedIDs {
		feedsDescriptionBits = append(feedsDescriptionBits, fmt.Sprintf("- <code>%s</code> (%s)", feedMap[f].ID, feedMap[f].Title))
	}
	feedsDescription := strings.Join(feedsDescriptionBits, "\n")

	return fmt.Sprintf(`<b>Episode #<code>%s</code> (%s)</b>

<b>Source:</b>
<code>%s</code>

<b>Files:</b>
<pre>%s</pre>

<b>Published to feeds:</b>
%s`,
		ep.ID,
		ep.Title,
		ep.SourceURL,
		strings.Join(ep.SourceFilepaths, ", "),
		feedsDescription,
	)
}

func (ub *UndercastBot) renderEpisodeShort(ep *service.Episode) string {
	return fmt.Sprintf(`<b>Episode #<code>%s</code> (%s)</b> [info: /ep_%s] [edit: /ee_%s]`, ep.ID, ep.Title, ep.ID, ep.ID)
}
