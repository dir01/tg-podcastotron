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
	"undercast-bot/service"
)

const editEpisodesHelp = `
<b>Edit episodes:</b>
<code>/editEpisodes_&lt;episode_id&gt;_to_&lt;episode_id&gt;</code>
or
<code>/editEpisodes_&lt;episode_id&gt;</code>

<b>Possible actions:</b>
- <code>Rename Episodes</code> - renames all episodes. 
If episodes contain a number, you can use <code>%%n</code> to insert the number in the new name
<i>Example: given episodes "Episode 1", "Episode 2", "Episode 3", renaming to "My Fairy Tale - Chapter %%n" will result in "My Fairy Tale - Chapter 1", "My Fairy Tale - Chapter 2", "My Fairy Tale - Chapter 3"</i>
(if episodes contain multiple numbers, the last one will be considered the episode number)

- <code>Manage Episodes Feeds</code> - allows you to add or remove episodes from feeds. Episodes can be added to multiple feeds

- <code>Delete Episodes</code> - delete episodes from your library, remove them from feeds and delete their files from disk
`

func (ub *UndercastBot) editEpisodesHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := ub.extractUsername(update)
	chatID := update.Message.Chat.ID

	zapFields := []zap.Field{
		zap.Int64("chatID", chatID),
		zap.String("messageText", update.Message.Text),
		zap.String("username", userID),
	}

	epIDs, err := ub.parseEditEpisodesCmd(update.Message.Text)
	if err != nil {
		ub.sendTextMessage(ctx, chatID, editEpisodesHelp)
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to parse /editEpisodes command", zapFields...))
		return
	}
	zapFields = append(zapFields, zap.Strings("episodeIDs", epIDs))

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

	ub.bot.RegisterHandler(bot.HandlerTypeCallbackQueryData, prefix, bot.MatchTypePrefix, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		st := strings.ReplaceAll(update.CallbackQuery.Data, prefix, "")

		switch st {
		case cmdRename:
			if msg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:      chatID,
				Text:        "Please enter new name for the episodes",
				ParseMode:   models.ParseModeHTML,
				ReplyMarkup: &models.ForceReply{ForceReply: true},
			}); err != nil {
				zapFields = append(zapFields, zap.Any("message", msg))
				ub.handleError(ctx, update.Message.Chat.ID, zaperr.Wrap(err, "failed to send message", zapFields...))
				return
			} else {
				ub.bot.RegisterHandlerMatchFunc(
					bot.HandlerTypeMessageText,
					func(update *models.Update) bool {
						return update.Message.ReplyToMessage != nil && update.Message.ReplyToMessage.ID == msg.ID
					},
					func(ctx context.Context, b *bot.Bot, update *models.Update) {
						newTitlePattern := update.Message.Text
						if err := ub.service.RenameEpisodes(ctx, epIDs, newTitlePattern, userID); err != nil {
							ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to rename episodes", zapFields...))
							return
						}

						msgTextParts := []string{fmt.Sprintf("%d episodes were renames", len(epIDs))}
						newEpisodesMap, err := ub.service.GetEpisodesMap(ctx, epIDs, userID)
						if err == nil {
							for _, epID := range epIDs {
								oldEp := episodesMap[epID]
								newEp := newEpisodesMap[epID]
								msgTextParts = append(msgTextParts, fmt.Sprintf("%s -> %s", oldEp.Title, newEp.Title))
							}
						}
						_, _ = ub.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
							ChatID:    chatID,
							MessageID: msg.ID,
							Text:      strings.Join(msgTextParts, "\n"),
						})
						_, _ = ub.bot.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
							ChatID:      chatID,
							MessageID:   msg.ID,
							ReplyMarkup: &models.InlineKeyboardMarkup{},
						})
					})
			}
		case cmdDelete:
			if err := ub.service.DeleteEpisodes(ctx, epIDs, userID); err != nil {
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to delete episodes", zapFields...))
				return
			}
		}
	})

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

	if msg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        initialMessageText,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: kb},
	}); err != nil {
		zapFields = append(zapFields, zap.Any("message", msg))
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zapFields...))
		return
	}
}

func (ub *UndercastBot) formatInitialMessage(epIDs []string, episodesMap map[string]*service.Episode, feedMap map[string]*service.Feed) (string, error) {
	var initialMessageParts []string
	for _, epID := range epIDs {
		ep := episodesMap[epID]
		if ep == nil {
			return "", zaperr.New("episode not found")
		}
		initialMessageParts = append(initialMessageParts, ub.renderEpisodeShort(ep, feedMap)) // TODO: split into multiple messages if too long
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

func (ub *UndercastBot) parseEditEpisodesCmd(text string) (epIDs []string, err error) {
	re, err := regexp.Compile(`/editEpisodes_(.*)`)
	if err != nil {
		return nil, fmt.Errorf("failed to compile regexp: %w", err)
	}

	matches := re.FindStringSubmatch(text)
	if len(matches) != 2 {
		return nil, fmt.Errorf(`failed to extract episode ids from the message '%s'`, text)
	}

	epIDs, err = parseIDs(matches[1])
	if err != nil {
		return nil, fmt.Errorf("failed to parse episode ids: %w", err)
	}

	return epIDs, nil
}
