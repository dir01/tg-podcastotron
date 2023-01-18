package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
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
	chatID := ub.extractChatID(update)

	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      editFeedsHelp,
		ParseMode: models.ParseModeHTML,
	}); err != nil {
		ub.logger.Error("sendTextMessage error", zap.Error(err))
	}

	ub.sendTextMessage(ctx, chatID, "Not implemented yet")
}
