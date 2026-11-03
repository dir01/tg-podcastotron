package bot

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tg-podcastotron/service"
)

// updateGroupMessage shows the status counts of all episodes from one create request
// in a single Telegram message: it sends that message the first time and edits it
// after. The message reference is kept in the episode message of the first episode.
func (ub *UndercastBot) updateGroupMessage(
	ctx context.Context,
	userID string,
	chatID int64,
	episodes []*service.Episode,
	defaultFeed *service.Feed,
) {
	keyEpID := episodes[0].ID
	fields := []any{slog.String("request_id", episodes[0].RequestID()), slog.String("episode_id", keyEpID)}

	rec, err := ub.repository.GetEpisodeMessage(ctx, userID, keyEpID)
	if err != nil {
		ub.logger.ErrorContext(ctx, "failed to load group message", append(fields, slog.Any("error", err))...)
		// continue with rec == nil: send a new message
	}

	var footerFeed *service.Feed
	if allEpisodesFinished(episodes) && countStatus(episodes, service.EpisodeStatusComplete) > 0 {
		footerFeed = defaultFeed
	}
	text := renderGroupMessage(episodes, time.Now().UTC(), footerFeed)

	if rec != nil && rec.MessageID != 0 {
		_, err := ub.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:             chatID,
			MessageID:          rec.MessageID,
			Text:               text,
			ParseMode:          models.ParseModeHTML,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()},
		})
		switch {
		case err == nil, isMessageNotModifiedError(err):
			return // the message ID did not change
		case bot.IsTooManyRequestsError(err):
			// A new message would also get 429. The next status change edits the message.
			ub.logger.WarnContext(ctx, "rate limited, skipping group message edit", append(fields, slog.Any("error", err))...)
			return
		}
		// The original message may have been deleted; start a new one.
		ub.logger.WarnContext(ctx, "failed to edit group message, sending a new one", append(fields, slog.Any("error", err))...)
	}

	messageID := ub.sendEpisodeLogMessage(ctx, chatID, text)
	if messageID == 0 {
		return // send failed; nothing useful to persist
	}
	if err := ub.repository.SaveEpisodeMessage(ctx, userID, keyEpID, &EpisodeMessage{
		ChatID:    chatID,
		MessageID: messageID,
		Log:       "[]",
	}); err != nil {
		ub.logger.ErrorContext(ctx, "failed to save group message", append(fields, slog.Any("error", err))...)
	}
}

// isMessageNotModifiedError tells if Telegram refused an edit because the text is the same.
// This occurs when a status change does not change the counts, for example a new attempt.
func isMessageNotModifiedError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

// groupStatusRows sets the order and the labels of the status counts.
var groupStatusRows = []struct {
	label    string
	statuses []service.EpisodeStatus
}{
	{"queued", []service.EpisodeStatus{service.EpisodeStatusCreated, service.EpisodeStatusPending}},
	{"downloading", []service.EpisodeStatus{service.EpisodeStatusDownloading}},
	{"encoding", []service.EpisodeStatus{service.EpisodeStatusProcessing}},
	{"uploading", []service.EpisodeStatus{service.EpisodeStatusUploading}},
	{"failed, will retry", []service.EpisodeStatus{service.EpisodeStatusRetrying}},
	{"done ✅", []service.EpisodeStatus{service.EpisodeStatusComplete}},
	{"failed ❌", []service.EpisodeStatus{service.EpisodeStatusFailed}},
}

func countStatus(episodes []*service.Episode, statuses ...service.EpisodeStatus) int {
	n := 0
	for _, ep := range episodes {
		for _, s := range statuses {
			if ep.Status == s {
				n++
				break
			}
		}
	}
	return n
}

func allEpisodesFinished(episodes []*service.Episode) bool {
	return countStatus(episodes, service.EpisodeStatusComplete, service.EpisodeStatusFailed) == len(episodes)
}

const groupMessageTimeLayout = "2006-01-02 15:04 UTC"

// renderGroupMessage builds the group message. It shows only status counts and the start
// and finish times, so its length does not depend on the number of episodes.
// now is the finish time if all episodes are finished.
func renderGroupMessage(episodes []*service.Episode, now time.Time, footerFeed *service.Feed) string {
	var b strings.Builder

	sourceURL := episodes[0].SourceURL
	if title := service.TitleFromSourceURL(sourceURL); title != "" {
		fmt.Fprintf(&b, "<b>%s</b>\n", html.EscapeString(truncateRunes(title, maxEpisodeLogHeaderLen)))
	}
	if sourceURL != "" {
		fmt.Fprintf(&b, "<code>%s</code>\n", html.EscapeString(truncateRunes(sourceURL, maxEpisodeLogHeaderLen)))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "<b>%d episodes</b>\n", len(episodes))
	counted := 0
	for _, row := range groupStatusRows {
		if n := countStatus(episodes, row.statuses...); n > 0 {
			fmt.Fprintf(&b, "%d %s\n", n, row.label)
			counted += n
		}
	}
	if other := len(episodes) - counted; other > 0 {
		fmt.Fprintf(&b, "%d other\n", other)
	}

	startedAt := episodes[0].CreatedAt
	for _, ep := range episodes {
		if ep.CreatedAt.Before(startedAt) {
			startedAt = ep.CreatedAt
		}
	}
	fmt.Fprintf(&b, "\nStarted: %s\n", startedAt.UTC().Format(groupMessageTimeLayout))
	if allEpisodesFinished(episodes) {
		fmt.Fprintf(&b, "Finished: %s (took %s)\n", now.UTC().Format(groupMessageTimeLayout), formatElapsed(now.Sub(startedAt)))
	}

	if footerFeed != nil {
		fmt.Fprintf(&b, "\nPublished to <b>%s</b>\n", html.EscapeString(truncateRunes(footerFeed.Title, maxEpisodeLogHeaderLen)))
	}

	ids := make([]string, len(episodes))
	for i, ep := range episodes {
		ids[i] = ep.ID
	}
	if idsStr, err := formatIDsCompactly(ids); err == nil {
		fmt.Fprintf(&b, "\n/ee_%s to rename or change feed", idsStr)
	}

	return b.String()
}
