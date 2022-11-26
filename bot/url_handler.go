package bot

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
	"undercast-bot/bot/ui/treemultiselect"
	"undercast-bot/service"
)

func (ub *UndercastBot) urlHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	url := update.Message.Text
	isValid, err := ub.service.IsValidURL(ctx, url)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, fmt.Errorf("failed to check if URL is valid: %w", err))
		return
	}
	if !isValid {
		ub.sendTextMessage(ctx, update.Message.Chat.ID, "Invalid command or URL")
		return
	}

	metadata, err := ub.service.FetchMetadata(ctx, url)
	if err != nil {
		ub.handleError(ctx, update.Message.Chat.ID, fmt.Errorf("failed to fetch metadata: %w", err))
		return
	}

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
		ub.logger.Error("urlHandler error", zap.Error(err), zap.Any("msg", msg))
	}
}

func (ub *UndercastBot) createEpisodes(ctx context.Context, url string, filepaths [][]string, chatID int, userID string) {
	if err := ub.service.CreateEpisodesAsync(ctx, url, filepaths, userID, chatID); err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to enqueue episodes creation: %w", err))
	}
}

func (ub *UndercastBot) onEpisodesCreated(ctx context.Context, createdEpisodes []*service.Episode) {
	userID := createdEpisodes[0].UserID // ¯\_(ツ)_/¯
	chatID, err := ub.store.GetChatID(ctx, userID)

	defaultFeed, err := ub.service.DefaultFeed(ctx, userID)
	if err != nil {
		ub.logger.Error("onEpisodesCreated failed to get default feed", zap.Error(err))
	}

	for _, ep := range createdEpisodes {
		err := ub.service.PublishEpisode(ctx, ep.ID, defaultFeed.ID, userID)
		if err != nil {
			ub.logger.Error("onEpisodesCreated failed to publish episode", zap.Error(err))
		}
	}

	episodeIDsStr, err := formatEpisodeIDs(createdEpisodes)
	if err != nil {
		ub.logger.Error("failed to format episode IDs", zap.Error(err))
	}

	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text: fmt.Sprintf(
			`%d episodes are scheduled.
After they are processed, they will be published to default feed:
<b>%s</b>
%s

To unpublish them from default feed, send command

/unpublish_ep_%s_from_%s

To publish them to another feed, send command

/publish_ep_%s`,
			len(createdEpisodes), defaultFeed.Title, defaultFeed.URL,
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

func formatEpisodeIDs(createdEpisodes []*service.Episode) (string, error) {
	var episodeIDs []string
	for _, episode := range createdEpisodes {
		episodeIDs = append(episodeIDs, episode.ID)
	}
	formatted, err := formatIDsCompactly(episodeIDs)
	return formatted, err
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
