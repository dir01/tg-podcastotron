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
	"golang.org/x/exp/slices"
	"tg-podcastotron/bot/ui/multiselect"
	"tg-podcastotron/service"
)

const editEpisodesHelp = `
<b>Edit episodes:</b>
<code>/ee_</code>&lt;episode_id&gt;
or
<code>/ee_</code>&lt;episode_id&gt;_to_&lt;episode_id&gt;

<b>Possible actions:</b>
- <b>Rename Episodes</b> - rename episodes. Use <code>%n</code> as placeholder for number as extracted from original name
- <b>Manage Episodes Feeds</b> - add or remove episodes from feeds
- <b>Delete Episodes</b> - delete episodes from your library, remove them from feeds and delete files from cloud storage
`

func (ub *UndercastBot) editEpisodesHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := ub.extractUserID(update)
	chatID := ub.extractChatID(update)

	zapFields := []zap.Field{
		zap.Int64("chat_id", chatID),
		zap.String("message_text", update.Message.Text),
		zap.String("user_id", userID),
		zap.String("username", ub.extractUsername(update)),
	}

	epIDs := ub.parseEditEpisodesCmd(update.Message.Text)
	if epIDs == nil {
		if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      editEpisodesHelp,
			ParseMode: models.ParseModeHTML,
		}); err != nil {
			zapFields := append(zapFields, zaperr.ToField(err))
			ub.logger.Error("sendTextMessage error", zapFields...)
		}
		return
	}
	zapFields = append(zapFields, zap.Strings("episode_ids", epIDs))

	episodesMap, err := ub.service.GetEpisodesMap(ctx, epIDs, userID)
	if err != nil {
		ub.sendTextMessage(ctx, chatID, "At least one of the episodes you are trying to edit does not exist. Please try again with different IDs")
		return
	}

	feeds, err := ub.service.ListFeeds(ctx, userID)
	if err != nil {
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to list feeds", zapFields...))
		return
	}
	var feedMap = make(map[string]*service.Feed)
	for _, feed := range feeds {
		feedMap[feed.ID] = feed
	}

	initialMessageText, err := ub.formatInitialMessage(epIDs, episodesMap, feedMap)
	if err != nil {
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to format initial message", zapFields...))
		return
	}

	prefix := fmt.Sprintf("editEpisodes_%s_%s", userID, bot.RandomString(10))
	cmdRename := "rename"
	cmdDelete := "delete"
	cmdManageFeeds := "manageFeeds"

	kb := [][]models.InlineKeyboardButton{
		{{
			Text:         "Rename Episodes",
			CallbackData: prefix + cmdRename,
		}},
		{{
			Text:         "Manage Episodes Feeds",
			CallbackData: prefix + cmdManageFeeds,
		}},
		{{
			Text:         "Delete Episodes",
			CallbackData: prefix + cmdDelete,
		}},
	}

	initialMsg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        initialMessageText,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: kb},
	})
	if err != nil {
		zapFields = append(zapFields, zap.Any("message", initialMsg))
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zapFields...))
		return
	}

	deleteInitialMessage := func() {
		if _, err := ub.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    chatID,
			MessageID: initialMsg.ID,
		}); err != nil {
			zapFields := append(zapFields, zaperr.ToField(err))
			ub.logger.Error("failed to delete initial message", zapFields...)
		}
	}

	ub.bot.RegisterHandler(bot.HandlerTypeCallbackQueryData, prefix, bot.MatchTypePrefix, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		st := strings.ReplaceAll(update.CallbackQuery.Data, prefix, "")

		switch st {
		case cmdRename:
			if renamePromptMsg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:      chatID,
				Text:        "Please enter new name for the episodes",
				ParseMode:   models.ParseModeHTML,
				ReplyMarkup: &models.ForceReply{ForceReply: true},
			}); err != nil {
				zapFields = append(zapFields, zap.Any("message", renamePromptMsg))
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zapFields...))
				return
			} else {
				ub.bot.RegisterHandlerMatchFunc(
					bot.HandlerTypeMessageText,
					func(update *models.Update) bool {
						return update.Message.ReplyToMessage != nil && update.Message.ReplyToMessage.ID == renamePromptMsg.ID
					},
					func(ctx context.Context, b *bot.Bot, update *models.Update) {
						newTitlePattern := update.Message.Text
						if err := ub.service.RenameEpisodes(ctx, epIDs, newTitlePattern, userID); err != nil {
							ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to rename episodes", zapFields...))
							return
						}

						if _, err = ub.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: renamePromptMsg.ID}); err != nil {
							zapFields := append(zapFields, zaperr.ToField(err))
							ub.logger.Error("failed to delete rename prompt message", zapFields...)
						}

						msgTextParts := []string{fmt.Sprintf("%d episodes were renamed", len(epIDs))}
						newEpisodesMap, err := ub.service.GetEpisodesMap(ctx, epIDs, userID)
						if err == nil {
							for _, epID := range epIDs {
								oldEp := episodesMap[epID]
								newEp := newEpisodesMap[epID]
								msgTextParts = append(msgTextParts, fmt.Sprintf("%s -> %s", oldEp.Title, newEp.Title))
							}
						}
						ub.sendTextMessage(ctx, chatID, strings.Join(msgTextParts, "\n"))
					})
			}
		case cmdDelete:
			if err := ub.service.DeleteEpisodes(ctx, epIDs, userID); err != nil {
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to delete episodes", zapFields...))
				return
			}

			statusMsgText := formatEpisodesDeletedStatusMessage(epIDs)

			ub.sendTextMessage(ctx, chatID, statusMsgText)

			deleteInitialMessage()
		case cmdManageFeeds:
			items := make([]*multiselect.Item, len(feeds))
			for i, feed := range feeds {
				selected := false
				for _, ep := range episodesMap {
					if slices.Contains(ep.FeedIDs, feed.ID) {
						selected = true
						break
					}
				}
				items[i] = &multiselect.Item{
					ID:       feed.ID,
					Text:     feed.Title,
					Selected: selected,
				}
			}
			feedSelector := multiselect.New(
				ub.bot,
				items,
				func(ctx context.Context, b *bot.Bot, mes *models.Message, items []*multiselect.Item) {

					feedIDs := make([]string, 0, len(items))
					for _, item := range items {
						if item.Selected {
							feedIDs = append(feedIDs, item.ID)
						}
					}

					if err := ub.service.PublishEpisodes(ctx, epIDs, feedIDs, userID); err != nil {
						ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to set episodes feeds", zapFields...))
						return
					}

					statusMsgText := formatManageFeedsStatusMessage(epIDs, feedIDs)

					ub.sendTextMessage(ctx, chatID, statusMsgText)

					deleteInitialMessage()
				},
				multiselect.WithItemFilters(multiselect.ItemFilter{}),
			)
			if _, err = ub.bot.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:      chatID,
				Text:        "Select feeds to add/remove",
				ParseMode:   models.ParseModeHTML,
				ReplyMarkup: feedSelector,
			}); err != nil {
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zapFields...))
			}
		}
	})

}

func formatEpisodesDeletedStatusMessage(epIDs []string) string {
	statusMsgText := fmt.Sprintf("Episode %s was deleted", epIDs[0])
	if len(epIDs) > 1 {
		statusMsgText = fmt.Sprintf("%d episodes were deleted (%s)", len(epIDs), strings.Join(epIDs, ", "))
	}
	return statusMsgText
}

func formatManageFeedsStatusMessage(epIDs []string, feedIDs []string) string {
	var statusMsgParts []string
	if len(epIDs) == 1 {
		statusMsgParts = []string{fmt.Sprintf("Episode %s was", epIDs[0])}
	} else {
		statusMsgParts = []string{fmt.Sprintf("%d episodes (%s) were", len(epIDs), strings.Join(epIDs, ", "))}
	}
	if len(feedIDs) == 1 {
		statusMsgParts = append(statusMsgParts, fmt.Sprintf("added to feed %s", feedIDs[0]))
	} else {
		statusMsgParts = append(statusMsgParts, fmt.Sprintf("added to %d feeds (%s)", len(feedIDs), strings.Join(feedIDs, ", ")))
	}
	statusMsgText := strings.Join(statusMsgParts, " ")
	return statusMsgText
}

func (ub *UndercastBot) formatInitialMessage(epIDs []string, episodesMap map[string]*service.Episode, feedMap map[string]*service.Feed) (string, error) {
	var initialMessageParts []string
	for _, epID := range epIDs {
		ep := episodesMap[epID]
		if ep == nil {
			return "", zaperr.New("episode not found")
		}
		initialMessageParts = append(initialMessageParts, ub.renderEpisodeShort(ep)) // TODO: split into multiple messages if too long
	}
	initialMessageParts = append(initialMessageParts, editEpisodesHelp)
	initialMessageText := strings.Join(initialMessageParts, "\n\n")

	if len(initialMessageText) > 4096 {
		return strings.Join([]string{
			fmt.Sprintf("You are editing %d episodes. Full info is too long to fit on one page. Try to select less episodes if you want to see what you are editing", len(epIDs)),
			editEpisodesHelp,
		}, "\n\n"), nil
	}

	return initialMessageText, nil
}

func (ub *UndercastBot) parseEditEpisodesCmd(text string) (epIDs []string) {
	re := regexp.MustCompile(`/ee_(.*)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) != 2 {
		return nil
	}

	if epIDs, err := parseIDs(matches[1]); err != nil {
		return nil
	} else {
		return epIDs
	}
}
