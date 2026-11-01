package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"tg-podcastotron/bot/ui/multiselect"
	"tg-podcastotron/bot/ui/treemultiselect"
	"tg-podcastotron/service"
	"tg-podcastotron/telemetry"
)

func (ub *UndercastBot) urlHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		ub.logger.ErrorContext(ctx, "urlHandler: update or update.Message is nil")
		return
	}

	chatID := ub.extractChatID(update)
	userID := ub.extractUserID(update)

	if update.Message == nil {
		return
	}
	url := update.Message.Text
	isValid, err := ub.service.IsValidURL(ctx, url)
	if err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to check if URL is valid: %w", err))
		return
	}
	if !isValid {
		ub.sendTextMessage(ctx, chatID, "Invalid command or URL")
		return
	}

	metadata, err := ub.service.FetchMetadata(ctx, url)
	if err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to fetch metadata: %w", err))
		return
	}

	switch metadata.DownloaderName {
	case "torrent":
		if err = ub.startTorrentFlow(ctx, metadata, userID, chatID); err != nil {
			ub.handleError(ctx, chatID, fmt.Errorf("failed to start torrent flow: %w", err))
			return
		}
	case "ytdl":
		if err = ub.startYtdlFlow(ctx, metadata, userID, chatID); err != nil {
			ub.handleError(ctx, chatID, fmt.Errorf("failed to start ytdl flow: %w", err))
			return
		}
	default:
		ub.sendTextMessage(ctx, chatID, fmt.Sprintf("Unsupported downloader: %s", metadata.DownloaderName))
		return
	}

}

func (ub *UndercastBot) startTorrentFlow(ctx context.Context, metadata *service.Metadata, userID string, chatID int64) error {
	var variants []string
	for _, v := range metadata.Variants {
		variants = append(variants, v.ID)
	}

	kb := treemultiselect.New(
		ub.bot,
		variants,
		nil, // onConfirmSelection is not needed if WithDynamicActionButtons is set
		treemultiselect.WithMaxNodesPerPage(10),
		treemultiselect.WithDynamicFilterButtons(func(selectedNodes []*treemultiselect.TreeNode) []treemultiselect.FilterButton {
			var buttons []treemultiselect.FilterButton
			topExts := getNTopExtensions(selectedNodes, 1)
			for _, ext := range topExts {
				buttons = append(buttons, treemultiselect.FilterButton{
					Text: "Select *." + ext,
					Fn: func(node *treemultiselect.TreeNode) bool {
						return strings.HasSuffix(node.Value, ext)
					},
				})
			}
			if len(buttons) > 0 {
				buttons = append(buttons, treemultiselect.FilterButtonSelectNone)
			}
			return buttons
		}),
		treemultiselect.WithDynamicActionButtons(func(selectedNodes []*treemultiselect.TreeNode) [][]treemultiselect.ActionButton {
			cancelBtn := treemultiselect.NewCancelButton("Cancel", func(ctx context.Context, bot *bot.Bot, mes *models.Message) {})

			switch len(selectedNodes) {
			case 0:
				return [][]treemultiselect.ActionButton{{cancelBtn}}
			case 1:
				return [][]treemultiselect.ActionButton{
					{treemultiselect.NewConfirmButton(
						"Create Episode",
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {
							ub.createEpisodes(ctx, userID, mes.Chat.ID, metadata.URL, [][]string{{paths[0]}}, service.ProcessingTypeUploadOriginal)
						},
					)},
					{cancelBtn},
				}
			default:
				return [][]treemultiselect.ActionButton{
					{treemultiselect.NewConfirmButton(
						fmt.Sprintf("Separate Episodes (%d)", len(selectedNodes)),
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {
							episodesPaths := make([][]string, len(paths))
							for i, path := range paths {
								episodesPaths[i] = []string{path}
							}
							ub.createEpisodes(ctx, userID, mes.Chat.ID, metadata.URL, episodesPaths, service.ProcessingTypeUploadOriginal)
						},
					)},
					{treemultiselect.NewConfirmButton(
						"Glue Into 1 Episode",
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {
							ub.createEpisodes(ctx, userID, mes.Chat.ID, metadata.URL, [][]string{paths}, service.ProcessingTypeConcatenate)
						},
					)},
					{cancelBtn},
				}
			}
		}),
	)

	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        "Please choose which files to include in the episode",
		ReplyMarkup: kb,
	}); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

func (ub *UndercastBot) startYtdlFlow(ctx context.Context, metadata *service.Metadata, userID string, chatID int64) error {
	items := make([]*multiselect.Item, len(metadata.Variants))
	for i, v := range metadata.Variants {
		items[i] = &multiselect.Item{ID: v.ID, Text: v.ID}
	}

	kb := multiselect.New(
		ub.bot,
		items,
		func(ctx context.Context, bot *bot.Bot, mes *models.Message, items []*multiselect.Item) {
			var variant string
			for _, item := range items {
				if item.Selected {
					variant = item.ID
					break
				}
			}
			ub.createEpisodes(ctx, userID, mes.Chat.ID, metadata.URL, [][]string{{variant}}, service.ProcessingTypeUploadOriginal)
		},
		multiselect.WithOnItemSelectedHandler(func(itemID string) *multiselect.StateChange {
			for _, v := range items {
				v.Selected = v.ID == itemID
			}
			return &multiselect.StateChange{Items: items}
		}),
		multiselect.WithItemFilters(),
	)

	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        "Please choose variant",
		ReplyMarkup: kb,
	}); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

func (ub *UndercastBot) createEpisodes(ctx context.Context, userID string, chatID int64, url string, variants [][]string, processingType service.ProcessingType) {
	if err := ub.service.CreateEpisodesAsync(ctx, userID, url, variants, processingType); err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to enqueue episodes creation: %w", err))
	}
}

// handleEpisodesStatusChanges runs in the trace of the job that made the changes.
// Without this, the logs of this handler have no trace_id.
func (ub *UndercastBot) handleEpisodesStatusChanges(ctx context.Context, batch service.EpisodeStatusChanges) {
	ctx = trace.ContextWithSpanContext(ctx, batch.SpanContext)
	ctx, span := telemetry.StartSpan(ctx, "bot.episodes_status_changes",
		attribute.Int("episodes.count", len(batch.Changes)))
	defer span.End()

	ub.onEpisodesStatusChanges(ctx, batch.Changes)
}

func (ub *UndercastBot) onEpisodesStatusChanges(ctx context.Context, episodeStatusChanges []service.EpisodeStatusChange) {
	// Preserve arrival order while grouping by user.
	var userOrder []string
	changesByUser := make(map[string][]service.EpisodeStatusChange)
	for _, change := range episodeStatusChanges {
		userID := change.Episode.UserID
		if _, exists := changesByUser[userID]; !exists {
			userOrder = append(userOrder, userID)
		}
		changesByUser[userID] = append(changesByUser[userID], change)
	}

	for _, userID := range userOrder {
		userChanges := changesByUser[userID]

		chatID, err := ub.repository.GetChatID(ctx, userID) // TODO: change to bulk get
		if err != nil {
			ub.handleError(ctx, 0, fmt.Errorf("failed to get chatID: %w", err))
			continue
		}

		defaultFeed, err := ub.service.DefaultFeed(ctx, userID)
		if err != nil {
			ub.logger.ErrorContext(ctx, "failed to get default feed", slog.Any("error", err))
		}

		// Newly created episodes are auto-published to the default feed (batched).
		var createdEpIDs []string
		for _, change := range userChanges {
			if change.NewStatus == service.EpisodeStatusCreated {
				createdEpIDs = append(createdEpIDs, change.Episode.ID)
			}
		}
		if len(createdEpIDs) > 0 && defaultFeed != nil {
			if err := ub.service.PublishEpisodes(ctx, userID, createdEpIDs, []string{defaultFeed.ID}); err != nil {
				ub.logger.ErrorContext(ctx, "failed to publish created episodes to default feed", slog.Any("error", err))
			}
		}

		// Each episode owns a single status-log message that we append to and edit in place.
		for _, change := range userChanges {
			ub.updateEpisodeLog(ctx, userID, chatID, change, defaultFeed)
		}
	}
}

type episodeLogEntry struct {
	At     time.Time `json:"at"`
	Status string    `json:"status"`
	// Detail is the reason of a failed attempt. It is shown below the status.
	Detail string `json:"detail,omitempty"`
}

// updateEpisodeLog appends the latest status to the episode's log and reflects it
// in a single Telegram message: it sends that message the first time and edits it
// in place on every subsequent status change.
func (ub *UndercastBot) updateEpisodeLog(
	ctx context.Context,
	userID string,
	chatID int64,
	change service.EpisodeStatusChange,
	defaultFeed *service.Feed,
) {
	epID := change.Episode.ID

	rec, err := ub.repository.GetEpisodeMessage(ctx, userID, epID)
	if err != nil {
		ub.logger.ErrorContext(ctx, "failed to load episode message", slog.String("episode_id", epID), slog.Any("error", err))
		// fall through with rec == nil: we'll start a fresh message
	}

	var entries []episodeLogEntry
	if rec != nil && rec.Log != "" {
		if err := json.Unmarshal([]byte(rec.Log), &entries); err != nil {
			ub.logger.ErrorContext(ctx, "failed to unmarshal episode log", slog.String("episode_id", epID), slog.Any("error", err))
			entries = nil
		}
	}
	entries = append(entries, newEpisodeLogEntry(time.Now().UTC(), change))

	var footerFeed *service.Feed
	if change.NewStatus == service.EpisodeStatusComplete {
		footerFeed = defaultFeed
	}
	text := renderEpisodeLog(change.Episode, entries, footerFeed)

	logJSON, err := json.Marshal(entries)
	if err != nil {
		ub.logger.ErrorContext(ctx, "failed to marshal episode log", slog.String("episode_id", epID), slog.Any("error", err))
		return
	}

	messageID := 0
	if rec != nil && rec.MessageID != 0 {
		messageID = rec.MessageID
		if _, err := ub.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:             chatID,
			MessageID:          rec.MessageID,
			Text:               text,
			ParseMode:          models.ParseModeHTML,
			LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()},
		}); err != nil {
			// The original message may have been deleted; start a new one.
			ub.logger.WarnContext(ctx, "failed to edit episode message, sending a new one", slog.String("episode_id", epID), slog.Any("error", err))
			messageID = ub.sendEpisodeLogMessage(ctx, chatID, text)
		}
	} else {
		messageID = ub.sendEpisodeLogMessage(ctx, chatID, text)
	}

	if messageID == 0 {
		return // send failed; nothing useful to persist
	}

	if err := ub.repository.SaveEpisodeMessage(ctx, userID, epID, &EpisodeMessage{
		ChatID:    chatID,
		MessageID: messageID,
		Log:       string(logJSON),
	}); err != nil {
		ub.logger.ErrorContext(ctx, "failed to save episode message", slog.String("episode_id", epID), slog.Any("error", err))
	}
}

func (ub *UndercastBot) sendEpisodeLogMessage(ctx context.Context, chatID int64, text string) int {
	msg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:             chatID,
		Text:               text,
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()},
	})
	if err != nil {
		ub.logger.ErrorContext(ctx, "failed to send episode message", slog.Int64("chat_id", chatID), slog.Any("error", err))
		return 0
	}
	return msg.ID
}

func episodeStatusLabel(status service.EpisodeStatus) string {
	switch status {
	case service.EpisodeStatusCreated:
		return "added"
	case service.EpisodeStatusPending:
		return "queued"
	case service.EpisodeStatusDownloading:
		return "downloading"
	case service.EpisodeStatusProcessing:
		return "encoding"
	case service.EpisodeStatusUploading:
		return "uploading"
	case service.EpisodeStatusComplete:
		return "done ✅"
	case service.EpisodeStatusFailed:
		return "failed ❌"
	case service.EpisodeStatusRetrying:
		return "failed, will retry"
	default:
		return string(status)
	}
}

func newEpisodeLogEntry(at time.Time, change service.EpisodeStatusChange) episodeLogEntry {
	label := episodeStatusLabel(change.NewStatus)
	attempt := formatAttempt(change.Attempt, change.MaxAttempts)
	switch change.NewStatus {
	case service.EpisodeStatusRetrying:
		if attempt != "" {
			label = attempt + " failed, will retry"
		}
	case service.EpisodeStatusComplete, service.EpisodeStatusFailed, service.EpisodeStatusCreated:
		// The attempt number is not important for these statuses.
	default:
		if change.Attempt > 1 {
			label += " (" + attempt + ")"
		}
	}
	return episodeLogEntry{At: at, Status: label, Detail: change.Error}
}

// maxEpisodeLogDetailLen limits each error in the status-log message. A Telegram message
// can have max 4096 characters. The header has the source URL, which can be long, and
// mediary can send an error for each of its attempts.
const maxEpisodeLogDetailLen = 200

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// formatAttempt returns "attempt 2 of 6", or "attempt 2" if the maximum is unknown.
// It returns "" if the attempt number is unknown.
func formatAttempt(attempt, maxAttempts int) string {
	if attempt <= 0 {
		return ""
	}
	if maxAttempts <= 0 {
		return fmt.Sprintf("attempt %d", attempt)
	}
	return fmt.Sprintf("attempt %d of %d", attempt, maxAttempts)
}

// maxEpisodeLogLen is the length budget of the status-log message. A Telegram
// message can have max 4096 characters, counted in UTF-16 code units after the
// HTML is parsed. The budget is a little smaller, to be safe.
const maxEpisodeLogLen = 4000

// maxEpisodeLogHeaderLen limits the title, the source URL and the feed title
// when the full message is too long.
const maxEpisodeLogHeaderLen = 200

// renderEpisodeLog builds the full status-log message. footerFeed, when non-nil,
// adds a "published to" footer (used once the episode is complete).
//
// If the message is too long for Telegram, it makes the message shorter in steps:
// it shortens the header, then it removes the details of all entries but the last,
// then it removes the oldest entries. The first and the last entry stay.
func renderEpisodeLog(ep *service.Episode, entries []episodeLogEntry, footerFeed *service.Feed) string {
	opts := episodeLogRenderOptions{headerLen: -1}
	text := renderEpisodeLogWith(ep, entries, footerFeed, opts)
	if fitsTelegram(text) {
		return text
	}

	opts.headerLen = maxEpisodeLogHeaderLen
	text = renderEpisodeLogWith(ep, entries, footerFeed, opts)
	if fitsTelegram(text) {
		return text
	}

	opts.lastDetailOnly = true
	text = renderEpisodeLogWith(ep, entries, footerFeed, opts)
	for !fitsTelegram(text) && opts.skipped < len(entries)-2 {
		opts.skipped++
		text = renderEpisodeLogWith(ep, entries, footerFeed, opts)
	}
	return text
}

type episodeLogRenderOptions struct {
	headerLen      int  // max runes of the title, the source URL and the feed title; -1 is no limit
	lastDetailOnly bool // show only the last detail
	skipped        int  // number of entries to remove after the first entry
}

func renderEpisodeLogWith(ep *service.Episode, entries []episodeLogEntry, footerFeed *service.Feed, opts episodeLogRenderOptions) string {
	header := func(s string) string {
		if opts.headerLen < 0 {
			return html.EscapeString(s)
		}
		return html.EscapeString(truncateRunes(s, opts.headerLen))
	}

	title := ep.Title
	if title == "" {
		title = "Episode #" + ep.ID
	}

	var b strings.Builder
	b.WriteString("<b>")
	b.WriteString(header(title))
	b.WriteString("</b>\n")
	if ep.SourceURL != "" {
		b.WriteString("<code>")
		b.WriteString(header(ep.SourceURL))
		b.WriteString("</code>\n")
	}
	b.WriteString("\n")

	var base time.Time
	if len(entries) > 0 {
		base = entries[0].At
	}
	lastDetail := -1
	for i, e := range entries {
		if e.Detail != "" {
			lastDetail = i
		}
	}
	for i, e := range entries {
		if i > 0 && i <= opts.skipped {
			if i == 1 {
				b.WriteString("…\n")
			}
			continue
		}
		b.WriteString(formatElapsed(e.At.Sub(base)))
		b.WriteString(" — ")
		b.WriteString(e.Status)
		b.WriteString("\n")
		if e.Detail != "" && (!opts.lastDetailOnly || i == lastDetail) {
			b.WriteString("    ↳ <i>")
			b.WriteString(html.EscapeString(truncateRunes(e.Detail, maxEpisodeLogDetailLen)))
			b.WriteString("</i>\n")
		}
	}

	if footerFeed != nil {
		b.WriteString("\nPublished to <b>")
		b.WriteString(header(footerFeed.Title))
		b.WriteString("</b>")
	}
	fmt.Fprintf(&b, "\n\n/ee_%s to rename or change feed", ep.ID)

	return b.String()
}

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// fitsTelegram tells if the HTML message is in the budget. Telegram counts the text
// after it parses the HTML: without the tags, and with the entities decoded. All
// content in the message is escaped, thus each "<" starts a tag.
func fitsTelegram(text string) bool {
	visible := html.UnescapeString(htmlTagRe.ReplaceAllString(text, ""))
	return len(utf16.Encode([]rune(visible))) <= maxEpisodeLogLen
}

func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

func getNTopExtensions(selectedNodes []*treemultiselect.TreeNode, n int) []string {
	extCounter := make(map[string]int)
	for _, n := range selectedNodes {
		extCounter[strings.TrimPrefix(filepath.Ext(n.Value), ".")]++
	}
	delete(extCounter, "") // files without extension don't interest us

	var topExts []string
	for i := 0; i < n; i++ {
		topExt := ""
		topCount := 0
		for ext, count := range extCounter {
			if count > topCount {
				topExt = ext
				topCount = count
			}
		}
		if topExt == "" {
			break
		}
		topExts = append(topExts, topExt)
		delete(extCounter, topExt)
	}
	return topExts
}
