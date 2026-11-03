package bot

import (
	"context"
	"errors"
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

func (ub *UndercastBot) listEpisodesHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := ub.extractUserID(update)
	chatID := ub.extractChatID(update)
	if userID == "" || chatID == 0 {
		return
	}

	epID := ub.parseListEpisodesCmd(update.Message.Text)

	var err error
	var episodes []*service.Episode
	feedMap := map[string]*service.Feed{}
	if epID == "" {
		if episodes, err = ub.service.ListUserEpisodes(ctx, userID); err != nil {
			ub.handleError(ctx, chatID, fmt.Errorf("failed to list episodes: %w", err))
			return
		}
	} else {
		if epMap, err := ub.service.GetEpisodesMap(ctx, userID, []string{epID}); err != nil {
			if errors.Is(err, service.ErrEpisodeNotFound) {
				ub.sendTextMessage(ctx, chatID, fmt.Sprintf("Episode %s not found", epID))
				return
			}
			ub.handleError(ctx, chatID, fmt.Errorf("failed to get episodes: %w", err))
			return
		} else {
			episodes = append(episodes, epMap[epID])
			if feeds, err := ub.service.ListFeeds(ctx, userID); err != nil {
				ub.handleError(ctx, chatID, fmt.Errorf("failed to list feeds: %w", err))
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
			ub.handleError(ctx, chatID, fmt.Errorf("failed to send message: %w", err))
		}
		return
	}

	params := &bot.SendMessageParams{ChatID: chatID, ParseMode: models.ParseModeHTML}
	if epID == "" {
		text, markup := ub.renderEpisodesPage(episodes, 0)
		params.Text = text
		if markup != nil {
			params.ReplyMarkup = markup
		}
	} else {
		ep := episodes[0]
		feeds, err := ub.service.ListEpisodeFeeds(ctx, userID, ep.ID)
		if err != nil {
			ub.handleError(ctx, chatID, fmt.Errorf("failed to list episode feeds: %w", err))
			return
		}
		params.Text = ub.renderEpisodeFull(ep, feeds)
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to send message: %w", err))
	}
}

// episodesPageCallbackPrefix starts the callback data of the page buttons: "epp:<page>".
const episodesPageCallbackPrefix = "epp:"

// episodesPageCallbackHandler shows a different page of the episode list in the same message.
func (ub *UndercastBot) episodesPageCallbackHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	cq := update.CallbackQuery
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID}); err != nil {
		ub.logger.ErrorContext(ctx, "failed to answer episodes page callback", slog.Any("error", err))
	}

	userID := ub.extractUserID(update)
	chatID := ub.extractChatID(update)
	msg := cq.Message.Message
	page, err := strconv.Atoi(strings.TrimPrefix(cq.Data, episodesPageCallbackPrefix))
	if err != nil || msg == nil {
		return
	}

	episodes, err := ub.service.ListUserEpisodes(ctx, userID)
	if err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to list episodes: %w", err))
		return
	}

	text, markup := ub.renderEpisodesPage(episodes, page)
	params := &bot.EditMessageTextParams{ChatID: chatID, MessageID: msg.ID, Text: text, ParseMode: models.ParseModeHTML}
	if markup != nil {
		params.ReplyMarkup = markup
	}
	if _, err := b.EditMessageText(ctx, params); err != nil && !isMessageNotModifiedError(err) {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to edit episodes message: %w", err))
	}
}

// renderEpisodesPage renders one page of the user's episodes.
// The markup is nil if all episodes fit on one page.
func (ub *UndercastBot) renderEpisodesPage(episodes []*service.Episode, page int) (string, *models.InlineKeyboardMarkup) {
	if len(episodes) == 0 {
		return "You have no episodes yet", nil
	}
	lines := make([]string, len(episodes))
	for i, ep := range episodes {
		lines[i] = ub.renderEpisodeShort(ep)
	}
	header := fmt.Sprintf("Episodes: %d", len(episodes))
	return renderPagedLines(header, lines, page, func(p int) string {
		return fmt.Sprintf("%s%d", episodesPageCallbackPrefix, p)
	})
}

func (ub *UndercastBot) parseListEpisodesCmd(text string) (epID string) {
	re := regexp.MustCompile(`/ep_(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

func (ub *UndercastBot) renderEpisodeFull(ep *service.Episode, feeds []*service.Feed) string {
	var feedsDescriptionBits []string
	for _, f := range feeds {
		feedsDescriptionBits = append(feedsDescriptionBits, fmt.Sprintf(
			"- <code>%s</code> (%s) [info: /f_%s] [edit: /ef_%s]", f.ID, html.EscapeString(f.Title), f.ID, f.ID,
		))
	}
	feedsDescription := strings.Join(feedsDescriptionBits, "\n")

	// A concatenated episode can carry hundreds of source files; the full list
	// easily exceeds Telegram's 4096-char message limit, so cap it and note how
	// many were hidden. Escape each path — filenames routinely contain & < >.
	escapedFiles := make([]string, len(ep.SourceFilepaths))
	for i, f := range ep.SourceFilepaths {
		escapedFiles[i] = html.EscapeString(f)
	}
	filesBlock := truncateJoin(escapedFiles, ", ", 3000)

	return fmt.Sprintf(`<b>Episode #<code>%s</code> (%s)</b>

<b>Source:</b>
<code>%s</code>

<b>Files:</b>
<pre>%s</pre>

<b>Published to feeds:</b>
%s`,
		ep.ID,
		html.EscapeString(ep.Title),
		html.EscapeString(ep.SourceURL),
		filesBlock,
		feedsDescription,
	)
}

func (ub *UndercastBot) renderEpisodeShort(ep *service.Episode) string {
	return fmt.Sprintf(`<b>Episode #<code>%s</code> (%s)</b> [info: /ep_%s] [edit: /ee_%s]`, ep.ID, html.EscapeString(ep.Title), ep.ID, ep.ID)
}

// truncateJoin joins items with sep, stopping before the result would exceed
// maxLen bytes and appending a note about how many items were omitted. Byte
// length is a safe over-estimate of Telegram's (UTF-16) character limit, so a
// budget comfortably under 4096 keeps the whole message within bounds.
func truncateJoin(items []string, sep string, maxLen int) string {
	var b strings.Builder
	shown := 0
	for _, it := range items {
		s := ""
		if shown > 0 {
			s = sep
		}
		if b.Len()+len(s)+len(it) > maxLen {
			break
		}
		b.WriteString(s)
		b.WriteString(it)
		shown++
	}
	if shown < len(items) {
		fmt.Fprintf(&b, "\n… and %d more file(s)", len(items)-shown)
	}
	return b.String()
}
