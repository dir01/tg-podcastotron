package bot

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

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
	userToStatusToChanges := make(map[string]map[service.EpisodeStatus][]service.EpisodeStatusChange)
	for _, change := range episodeStatusChanges {
		if _, exists := userToStatusToChanges[change.Episode.UserID]; !exists {
			userToStatusToChanges[change.Episode.UserID] = make(map[service.EpisodeStatus][]service.EpisodeStatusChange)
		}
		userToStatusToChanges[change.Episode.UserID][change.NewStatus] = append(userToStatusToChanges[change.Episode.UserID][change.NewStatus], change)
	}

	for userID, statusToChangesMap := range userToStatusToChanges {
		chatID, err := ub.repository.GetChatID(ctx, userID) // TODO: change to bulk get
		if err != nil {
			ub.handleError(ctx, 0, fmt.Errorf("failed to get chatID: %w", err))
			return
		}

		if createdMap, exists := statusToChangesMap[service.EpisodeStatusCreated]; exists && len(createdMap) > 0 {
			delete(statusToChangesMap, service.EpisodeStatusCreated)
			ub.handleEpisodesCreated(ctx, userID, chatID, createdMap)
		}

		var otherChanges []service.EpisodeStatusChange
		for _, changes := range statusToChangesMap {
			otherChanges = append(otherChanges, changes...)
		}
		if len(otherChanges) > 0 {
			ub.notifyStatusChanged(ctx, userID, chatID, otherChanges)
		}
	}
}

func (ub *UndercastBot) handleEpisodesCreated(ctx context.Context, userID string, chatID int64, changes []service.EpisodeStatusChange) {
	defaultFeed, err := ub.service.DefaultFeed(ctx, userID)
	if err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to get default feed: %w", err))
	}

	epIDs := make([]string, 0, len(changes))
	for _, statusChange := range changes {
		epIDs = append(epIDs, statusChange.Episode.ID)
	}

	if err := ub.service.PublishEpisodes(ctx, userID, epIDs, []string{defaultFeed.ID}); err != nil {
		ub.logger.ErrorContext(ctx, "handleEpisodesCreated failed to publish episodes", slog.Any("error", err))
	}

	message, err := formatEpisodesCreatedMessage(epIDs, defaultFeed)
	if err != nil {
		ub.logger.ErrorContext(ctx, "failed to format episodes created message", slog.Any("error", err))
		message = "Accepted"
	}
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        message,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: nil,
	}); err != nil {
		ub.logger.ErrorContext(ctx, "failed to send message",
			slog.String("user_id", userID),
			slog.Int64("chat_id", chatID),
			slog.Any("error", err),
		)
	}
}

func (ub *UndercastBot) notifyStatusChanged(ctx context.Context, userID string, chatID int64, changes []service.EpisodeStatusChange) {
	for _, change := range changes {
		ub.sendTextMessage(ctx, chatID, fmt.Sprintf("Episode #%s (%s) is now %s", change.Episode.ID, change.Episode.Title, change.NewStatus))
	}
}

func formatEpisodesCreatedMessage(epIDs []string, defaultFeed *service.Feed) (string, error) {
	if len(epIDs) == 0 {
		return "", nil
	}

	if len(epIDs) == 1 {
		return fmt.Sprintf(
			`Episode creation scheduled.
When it's ready, it will be published to default feed:

<b>%s</b>
<code>%s</code>

To change the feed or name, send /ee_%s`,
			defaultFeed.Title, defaultFeed.URL, epIDs[0],
		), nil
	}

	episodeIDsStr, err := formatIDsCompactly(epIDs)
	if err != nil {
		return "", fmt.Errorf("failed to format episode IDs: %w", err)
	}

	strBits := []string{
		fmt.Sprintf("%d episodes are scheduled.", len(epIDs)),
		"When they are ready, they will be published to default feed:",
		"",
		fmt.Sprintf("<b>%s</b>", defaultFeed.Title),
		fmt.Sprintf("<code>%s</code>", defaultFeed.URL),
		"",
		fmt.Sprintf("To change the feed or name, send /ee_%s", episodeIDsStr),
	}

	return strings.Join(strBits, "\n"), nil
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
