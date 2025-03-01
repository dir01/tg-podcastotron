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
	"tg-podcastotron/service"
)

func (ub *UndercastBot) listFeedsHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := ub.extractUserID(update)
	chatID := update.Message.Chat.ID
	if userID == "" || chatID == 0 {
		return
	}

	feedID := ub.parseListFeedsCmd(update.Message.Text)

	zapFields := []zap.Field{
		zap.Int64("chat_id", chatID),
		zap.String("message_text", update.Message.Text),
		zap.String("user_id", userID),
		zap.String("username", ub.extractUsername(update)),
		zap.String("feed_id", feedID),
	}

	feeds, err := ub.service.ListFeeds(ctx, userID)
	if err != nil {
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to list feeds", zapFields...))
		return
	}

	if feedID != "" {
		var feed *service.Feed
		for _, f := range feeds {
			if f.ID == feedID {
				feed = f
				break
			}
		}
		if feed == nil {
			ub.sendTextMessage(ctx, chatID, fmt.Sprintf("Feed %s not found", feedID))
			return
		}
		feeds = []*service.Feed{feed}
	}

	episodes, err := ub.service.ListUserEpisodes(ctx, userID)
	if err != nil {
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to list episodes", zapFields...))
		return
	}

	episodesMap := map[string]*service.Episode{}
	for _, ep := range episodes {
		episodesMap[ep.ID] = ep
	}

	for _, f := range feeds {
		var text string
		if feedID == "" {
			text = ub.renderFeedShort(f)
		} else {
			feedEpisodes, err := ub.service.ListFeedEpisodes(ctx, userID, feedID)
			if err != nil {
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to list feed episodes", zapFields...))
				return
			}
			text = ub.renderFeedFull(f, feedEpisodes)
		}
		if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      text,
			ParseMode: models.ParseModeHTML,
		}); err != nil {
			ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zapFields...))
		}
	}

}

func (ub *UndercastBot) renderFeedShort(f *service.Feed) string {
	return fmt.Sprintf(
		"Feed #<code>%s</code> - <b>%s</b> [info: /f_%s] [edit: /ef_%s]\n<code>%s</code>",
		f.ID, f.Title, f.ID, f.ID, f.URL,
	)
}

func (ub *UndercastBot) renderFeedFull(f *service.Feed, episodes []*service.Episode) string {
	var renderedEpisodesBits []string
	episodeIDs := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		renderedEpisodesBits = append(renderedEpisodesBits, ub.renderEpisodeShort(ep))
		episodeIDs = append(episodeIDs, ep.ID)
	}
	renderedEpisodes := strings.Join(renderedEpisodesBits, "\n")

	msgBits := []string{
		fmt.Sprintf(`Feed #<code>%s</code> - <b>%s</b> [info: /f_%s] [edit: /ef_%s]`, f.ID, f.Title, f.ID, f.ID),
		fmt.Sprintf(`<code>%s</code>`, f.URL),
		"",
	}
	if len(episodeIDs) > 0 {
		episodesTitle := fmt.Sprintf(`Episodes: %d`, len(episodeIDs))
		if formattedIDs, err := formatIDsCompactly(episodeIDs); err == nil {
			episodesTitle += " [edit: /ee_" + formattedIDs + "]"
		}
		msgBits = append(msgBits, episodesTitle)
		msgBits = append(msgBits, renderedEpisodes)
	} else {
		msgBits = append(msgBits, "No episodes yet")
	}

	return strings.Join(msgBits, "\n")
}

func (ub *UndercastBot) parseListFeedsCmd(text string) (epID string) {
	re := regexp.MustCompile(`/f_(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}
