package bot

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/hori-ryota/zaperr"
	"go.uber.org/zap"
	"undercast-bot/bot/ui/treemultiselect"
	"undercast-bot/service"
)

func (ub *UndercastBot) urlHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	zapFields := []zap.Field{
		zap.Int("chatID", update.Message.Chat.ID),
		zap.String("messageText", update.Message.Text),
	}

	if update == nil || update.Message == nil {
		return
	}
	url := update.Message.Text
	isValid, err := ub.service.IsValidURL(ctx, url)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to check if URL is valid", zapFields...))
		return
	}
	if !isValid {
		ub.sendTextMessage(ctx, update.Message.Chat.ID, "Invalid command or URL")
		return
	}

	metadata, err := ub.service.FetchMetadata(ctx, url)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to fetch metadata", zapFields...))
		return
	}

	zapFields = append(zapFields, zap.Any("metadata", metadata))

	var paths []string
	for _, file := range metadata.Files {
		paths = append(paths, file.Path)
	}

	kb := treemultiselect.New(
		ub.bot,
		paths,
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
		treemultiselect.WithDynamicActionButtons(func(selectedNodes []*treemultiselect.TreeNode) []treemultiselect.ActionButton {
			cancelBtn := treemultiselect.NewCancelButton("Cancel", func(ctx context.Context, bot *bot.Bot, mes *models.Message) {})

			switch len(selectedNodes) {
			case 0:
				return []treemultiselect.ActionButton{cancelBtn}
			case 1:
				return []treemultiselect.ActionButton{
					treemultiselect.NewConfirmButton(
						"Create Episode",
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {
							ub.createEpisodes(ctx, url, [][]string{{paths[0]}}, mes.Chat.ID, ub.extractUsername(update))
						},
					),
					cancelBtn,
				}
			default:
				return []treemultiselect.ActionButton{
					treemultiselect.NewConfirmButton(
						"1 File - 1 Episode",
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {
							episodesPaths := make([][]string, len(paths))
							for i, path := range paths {
								episodesPaths[i] = []string{path}
							}
							ub.createEpisodes(ctx, url, episodesPaths, mes.Chat.ID, ub.extractUsername(update))
						},
					),
					treemultiselect.NewConfirmButton(
						fmt.Sprintf("%d Files - 1 Episode", len(selectedNodes)),
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {
							ub.createEpisodes(ctx, url, [][]string{paths}, mes.Chat.ID, ub.extractUsername(update))
						},
					),
					cancelBtn,
				}
			}
		}),
	)

	if msg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        fmt.Sprintf("Please choose which files to include in the episode"),
		ReplyMarkup: kb,
	}); err != nil {
		zapFields = append(zapFields, zap.Any("message", msg))
		ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to send message", zapFields...))
		return
	}
}

func (ub *UndercastBot) createEpisodes(ctx context.Context, url string, filepaths [][]string, chatID int, userID string) {
	if err := ub.service.CreateEpisodesAsync(ctx, url, filepaths, userID); err != nil {
		ub.handleError(ctx, chatID, zaperr.Wrap(
			err, "failed to enqueue episodes creation",
			zap.Int("chatID", chatID),
			zap.String("userID", userID),
			zap.String("url", url),
			zap.Any("filepaths", filepaths),
		))
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
		chatID, err := ub.store.GetChatID(ctx, userID) // TODO: change to bulk get
		if err != nil {
			ub.handleError(ctx, 0, zaperr.Wrap(err, "failed to get chatID", zap.String("userID", userID)))
			return
		}

		if changesCreated, exists := statusToChangesMap[service.EpisodeStatusCreated]; exists && len(changesCreated) > 0 {
			delete(statusToChangesMap, service.EpisodeStatusCreated)
			ub.handleEpisodesCreated(ctx, userID, chatID, changesCreated)
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

func (ub *UndercastBot) handleEpisodesCreated(ctx context.Context, userID string, chatID int, changes []service.EpisodeStatusChange) {
	zapFields := []zap.Field{
		zap.String("userID", userID),
		zap.Int("chatID", chatID),
	}

	defaultFeed, err := ub.service.DefaultFeed(ctx, userID)
	if err != nil {
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to get default feed", zapFields...))
	}

	epIDs := make([]string, 0, len(changes))
	for _, statusChange := range changes {
		epIDs = append(epIDs, statusChange.Episode.ID)
	}
	if err := ub.service.PublishEpisodes(ctx, epIDs, defaultFeed.ID, userID); err != nil {
		ub.logger.Error("handleEpisodesCreated failed to publish episodes", zap.Error(err))
	}

	episodeIDsStr, err := formatIDsCompactly(epIDs)
	if err != nil {
		ub.logger.Error("failed to format episode IDs", zap.Error(err))
	}

	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text: fmt.Sprintf(`
%d episodes are scheduled.
After they are processed, they will be published to default feed:
<b>%s</b>
%s

To unpublish them from default feed, send command

/unpublish_ep_%s_from_%s

To publish them to another feed, send command

/publish_ep_%s`,
			len(changes), defaultFeed.Title, defaultFeed.URL,
			episodeIDsStr, defaultFeed.ID,
			episodeIDsStr,
		),
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: nil,
	}); err != nil {
		ub.logger.Error("failed to send message",
			zap.String("userID", userID),
			zap.Int("chatID", chatID),
			zap.Error(err),
		)
	}
}

func (ub *UndercastBot) notifyStatusChanged(ctx context.Context, userID string, chatID int, changes []service.EpisodeStatusChange) {
	for _, change := range changes {
		ub.sendTextMessage(ctx, chatID, "Episode #%s (%s) is now %s", change.Episode.ID, change.Episode.Title, change.NewStatus)
	}
}

func getNTopExtensions(selectedNodes []*treemultiselect.TreeNode, n int) []string {
	extCounter := make(map[string]int)
	for _, n := range selectedNodes {
		extCounter[strings.TrimPrefix(filepath.Ext(n.Value), ".")]++
	}
	delete(extCounter, "") // no-extension files

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
