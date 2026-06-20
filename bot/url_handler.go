package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tg-podcastotron/bot/ui/multiselect"
	"tg-podcastotron/bot/ui/treemultiselect"
	"tg-podcastotron/service"
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
	entries = append(entries, episodeLogEntry{At: time.Now().UTC(), Status: episodeStatusLabel(change.NewStatus)})

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
	default:
		return string(status)
	}
}

// renderEpisodeLog builds the full status-log message. footerFeed, when non-nil,
// adds a "published to" footer (used once the episode is complete).
func renderEpisodeLog(ep *service.Episode, entries []episodeLogEntry, footerFeed *service.Feed) string {
	title := ep.Title
	if title == "" {
		title = "Episode #" + ep.ID
	}

	var b strings.Builder
	b.WriteString("<b>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</b>\n")
	if ep.SourceURL != "" {
		b.WriteString("<code>")
		b.WriteString(html.EscapeString(ep.SourceURL))
		b.WriteString("</code>\n")
	}
	b.WriteString("\n")

	var base time.Time
	if len(entries) > 0 {
		base = entries[0].At
	}
	for _, e := range entries {
		b.WriteString(formatElapsed(e.At.Sub(base)))
		b.WriteString(" — ")
		b.WriteString(e.Status)
		b.WriteString("\n")
	}

	if footerFeed != nil {
		b.WriteString("\nPublished to <b>")
		b.WriteString(html.EscapeString(footerFeed.Title))
		b.WriteString("</b>")
	}
	fmt.Fprintf(&b, "\n\n/ee_%s to rename or change feed", ep.ID)

	return b.String()
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
