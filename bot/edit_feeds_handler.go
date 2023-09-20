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
)

const editFeedsHelp = `
<b>Edit feeds:</b>
<code>/ef_</code>&lt;feed_id&gt;

<b>Possible actions:</b>
- <b>Rename Feed</b> - renames your feed 
- <b>Delete Feed</b> - deletes your feed, but keeps the episodes in your library
- <b>Delete Feed and Episodes</b> - deletes your feed and all episodes in it from your library and disk
`

func (ub *UndercastBot) editFeedsHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	var editFeedsHelp = editFeedsHelp
	chatID := ub.extractChatID(update)
	userID := ub.extractUserID(update)

	zapFields := []zap.Field{
		zap.Int64("chat_id", chatID),
		zap.String("user_id", userID),
		zap.String("message_text", update.Message.Text),
	}

	feedID, err := ub.parseEditFeedsCmd(update.Message.Text)
	if err != nil {
		if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      editFeedsHelp,
			ParseMode: models.ParseModeHTML,
		}); err != nil {
			zapFields := append(zapFields, zaperr.ToField(err))
			ub.logger.Error("sendTextMessage error", zapFields...)
		}
		return
	}

	feed, err := ub.service.GetFeed(ctx, userID, feedID)
	if err != nil {
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to get feed", zapFields...))
		return
	}

	zapFields = append(zapFields, zap.String("feed_id", feedID))

	prefix := fmt.Sprintf("editFeed_%s_%s", userID, bot.RandomString(10))
	cmdRename := "rename"
	cmdDeleteFeed := "deleteFeed"
	cmdDeleteFeedAndEpisodes := "deleteFeedAndEpisodes"
	cmdMakePermanent := "makePermanent"
	cmdMakeEphemeral := "makeEphemeral"

	kb := [][]models.InlineKeyboardButton{
		{{
			Text:         "Rename Feed",
			CallbackData: prefix + cmdRename,
		}},
		{{
			Text:         "Delete Feed",
			CallbackData: prefix + cmdDeleteFeed,
		}},
		{{
			Text:         "Delete Feed and Episodes",
			CallbackData: prefix + cmdDeleteFeedAndEpisodes,
		}},
	}

	if isAdmin, _ := ub.auth.IsAdmin(ctx, ub.extractUsername(update)); isAdmin {
		editFeedsHelp += `- <b>Mark Permanent</b>/<b>Mark Ephemeral</b> - choose whether or not episodes should be auto-deleted after 30 days
`
		switch feed.IsPermanent {
		case true:
			kb = append(kb, []models.InlineKeyboardButton{{
				Text:         "Make Ephemeral",
				CallbackData: prefix + cmdMakeEphemeral,
			}})
		case false:
			kb = append(kb, []models.InlineKeyboardButton{{
				Text:         "Make Permanent",
				CallbackData: prefix + cmdMakePermanent,
			}})
		}
	}

	initialMessage, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        editFeedsHelp,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: kb},
	})
	if err != nil {
		ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to send message", zapFields...))
	}

	deleteInitialMessage := func() {
		if _, err := ub.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    chatID,
			MessageID: initialMessage.ID,
		}); err != nil {
			ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to delete message", zapFields...))
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
						newTitle := update.Message.Text
						if err := ub.service.RenameFeed(ctx, userID, feedID, newTitle); err != nil {
							ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to rename feed", zapFields...))
							return
						}

						if _, err = ub.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: renamePromptMsg.ID}); err != nil {
							zapFields := append(zapFields, zaperr.ToField(err))
							ub.logger.Error("failed to delete rename prompt message", zapFields...)
						}

						ub.sendTextMessage(ctx, chatID, fmt.Sprintf("Feed %s was renamed to \"%s\"", feedID, newTitle))

						deleteInitialMessage()
					})
			}

		case cmdDeleteFeed, cmdDeleteFeedAndEpisodes:
			shouldDeleteEpisodes := st == cmdDeleteFeedAndEpisodes

			if err := ub.service.DeleteFeed(ctx, userID, feedID, shouldDeleteEpisodes); err != nil {
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to delete episodes", zapFields...))
				return
			}

			replyText := fmt.Sprintf("Feed %s was deleted\n", feedID)
			if shouldDeleteEpisodes {
				replyText += "All feed episodes were deleted, too"
			} else {
				replyText += "All episodes are left in your library"
			}
			ub.sendTextMessage(ctx, chatID, replyText)

			deleteInitialMessage()

		case cmdMakePermanent:
			if err := ub.service.MarkFeedAsPermanent(ctx, userID, feedID); err != nil {
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to mark feed as permanent", zapFields...))
				return
			}

			ub.sendTextMessage(ctx, chatID, fmt.Sprintf("Feed #%s (%s) was marked as permanent", feedID, feed.Title))

			deleteInitialMessage()

		case cmdMakeEphemeral:
			if err := ub.service.MarkFeedAsEphemeral(ctx, userID, feedID); err != nil {
				ub.handleError(ctx, chatID, zaperr.Wrap(err, "failed to mark feed as ephemeral", zapFields...))
				return
			}

			ub.sendTextMessage(ctx, chatID, fmt.Sprintf("Feed #%s (%s) was marked as ephemeral", feedID, feed.Title))

			deleteInitialMessage()
		}
	})

}

func (ub *UndercastBot) parseEditFeedsCmd(text string) (string, error) {
	re := regexp.MustCompile(`/ef_(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) != 2 {
		return "", fmt.Errorf("invalid command")
	}
	return matches[1], nil
}
