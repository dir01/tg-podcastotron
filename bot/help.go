package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

const helpMessage = `
This bot allows you to create podcasts from torrent magnet links.

You just send it a link, choose files, choose if you want them glued together,
or published as separate episodes, and undercast will download the torrent,
convert the files, and publish them to your podcast.

You have a default podcast feed, but you can create as many as you want:

<code>/feeds</code> command will list all your podcasts, and 

<code>/addFeed</code> will create a new podcast

New episodes will be published your default podcast feed,
and bot will try to figure a title for them to the best of its ability,
but you can always edit them later: change title 
or which podcast feeds they are published to, like so

<code>/editEpisodes_12_to_28</code> - edit episodes 12-28

<code>/episodes</code> will list all your episodes

<code>/start</code> or <code>/help</code> will render this message
`

func (ub *UndercastBot) helpHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      helpMessage,
		ParseMode: models.ParseModeHTML,
	}); err != nil {
		ub.logger.Error("sendTextMessage error", zap.Error(err))
	}

}
