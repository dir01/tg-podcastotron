package bot

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

const helpMessage = `
This bot allows you to create podcasts from torrent magnet links.

<b>You just send it a link, choose files, and it will be published to your podcast feed</b>
Subscribe to it and listen away!

Bot will try to figure episode title to the best of its ability,
but you can always edit episodes later: change title 
or which podcast feeds they are published to, like so:

/ee_1- edit episode 1
/ee_1_to_10 - edit episodes 1 to 10

If you wonder where do you get episode IDs from, just run
/ep - list all your episodes

If you ever need more info about some episode, just run
/ep_1 - get more info about episode 1

If you want to have more than one podcast feed,
/nf will create a new podcast feed;
/ef_1 will edit podcast feed with ID 1;
/f will list all your podcast feeds;

/start or /help will render this message
`

func (ub *UndercastBot) helpHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    ub.extractChatID(update),
		Text:      helpMessage,
		ParseMode: models.ParseModeHTML,
	}); err != nil {
		ub.logger.Error("sendTextMessage error", zap.Error(err))
	}

}
