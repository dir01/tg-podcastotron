package bot

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tg-podcastotron/service"
)

func (ub *UndercastBot) listFeedsHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := ub.extractUserID(update)
	chatID := update.Message.Chat.ID
	if userID == "" || chatID == 0 {
		return
	}

	feedID := ub.parseListFeedsCmd(update.Message.Text)

	feeds, err := ub.service.ListFeeds(ctx, userID)
	if err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to list feeds: %w", err))
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

	for _, f := range feeds {
		params := &bot.SendMessageParams{ChatID: chatID, ParseMode: models.ParseModeHTML}
		if feedID == "" {
			params.Text = ub.renderFeedShort(f)
		} else {
			feedEpisodes, err := ub.service.ListFeedEpisodes(ctx, userID, feedID)
			if err != nil {
				ub.handleError(ctx, chatID, fmt.Errorf("failed to list feed episodes: %w", err))
				return
			}
			text, markup := ub.renderFeedPage(f, feedEpisodes, 0)
			params.Text = text
			if markup != nil {
				params.ReplyMarkup = markup
			}
		}
		if _, err := ub.bot.SendMessage(ctx, params); err != nil {
			ub.handleError(ctx, chatID, fmt.Errorf("failed to send message: %w", err))
		}
	}
}

// feedPageCallbackPrefix starts the callback data of the page buttons: "fp:<feedID>:<page>".
const feedPageCallbackPrefix = "fp:"

// feedPageCallbackHandler shows a different page of the feed in the same message.
func (ub *UndercastBot) feedPageCallbackHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	cq := update.CallbackQuery
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID}); err != nil {
		ub.logger.ErrorContext(ctx, "failed to answer feed page callback", slog.Any("error", err))
	}

	userID := ub.extractUserID(update)
	chatID := ub.extractChatID(update)
	msg := cq.Message.Message
	feedID, pageStr, _ := strings.Cut(strings.TrimPrefix(cq.Data, feedPageCallbackPrefix), ":")
	page, err := strconv.Atoi(pageStr)
	if err != nil || msg == nil {
		return
	}

	feeds, err := ub.service.ListFeeds(ctx, userID)
	if err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to list feeds: %w", err))
		return
	}
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
	feedEpisodes, err := ub.service.ListFeedEpisodes(ctx, userID, feedID)
	if err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to list feed episodes: %w", err))
		return
	}

	text, markup := ub.renderFeedPage(feed, feedEpisodes, page)
	params := &bot.EditMessageTextParams{ChatID: chatID, MessageID: msg.ID, Text: text, ParseMode: models.ParseModeHTML}
	if markup != nil {
		params.ReplyMarkup = markup
	}
	if _, err := b.EditMessageText(ctx, params); err != nil && !isMessageNotModifiedError(err) {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to edit feed message: %w", err))
	}
}

func (ub *UndercastBot) renderFeedShort(f *service.Feed) string {
	return fmt.Sprintf(
		"Feed #<code>%s</code> - <b>%s</b> [info: /f_%s] [edit: /ef_%s]\n<code>%s</code>",
		f.ID, html.EscapeString(f.Title), f.ID, f.ID, html.EscapeString(f.URL),
	)
}

// renderFeedPage renders the feed with one page of its episodes.
// The markup is nil if all episodes fit on one page.
func (ub *UndercastBot) renderFeedPage(f *service.Feed, episodes []*service.Episode, page int) (string, *models.InlineKeyboardMarkup) {
	header := []string{
		fmt.Sprintf(`Feed #<code>%s</code> - <b>%s</b> [info: /f_%s] [edit: /ef_%s]`, f.ID, html.EscapeString(f.Title), f.ID, f.ID),
		fmt.Sprintf(`<code>%s</code>`, html.EscapeString(f.URL)),
		"",
	}
	if len(episodes) == 0 {
		header = append(header, "No episodes yet")
		return strings.Join(header, "\n"), nil
	}

	lines := make([]string, len(episodes))
	episodeIDs := make([]string, len(episodes))
	for i, ep := range episodes {
		lines[i] = ub.renderEpisodeShort(ep)
		episodeIDs[i] = ep.ID
	}

	episodesTitle := fmt.Sprintf(`Episodes: %d`, len(episodes))
	// With many scattered IDs, the command can be too long for the message.
	if formattedIDs, err := formatIDsCompactly(episodeIDs); err == nil && len(formattedIDs) <= 300 {
		episodesTitle += " [edit: /ee_" + formattedIDs + "]"
	}
	header = append(header, episodesTitle)

	return renderPagedLines(strings.Join(header, "\n"), lines, page, func(p int) string {
		return fmt.Sprintf("%s%s:%d", feedPageCallbackPrefix, f.ID, p)
	})
}

func (ub *UndercastBot) parseListFeedsCmd(text string) (epID string) {
	re := regexp.MustCompile(`/f_(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}
