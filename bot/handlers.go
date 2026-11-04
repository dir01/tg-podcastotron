package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tg-podcastotron/auth"
	"tg-podcastotron/service"
)

// invitePrefix is the prefix of the /start parameter of an invite link.
const invitePrefix = "inv_"

const welcomeMessage = `Send me a magnet link or a link to a video. I make podcast episodes from it.

Open the app to choose the files, follow the progress, and manage your episodes and feeds.`

const helpMessage = `Send me a magnet link or a link to a video.

Open the app to see your episodes and feeds.`

// startHandler shows the welcome message. "/start inv_<token>" accepts an invite first.
func (ub *UndercastBot) startHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	msg := update.Message
	userID := strconv.FormatInt(msg.From.ID, 10)

	ok, err := ub.auth.IsAuthenticated(ctx, userID, msg.From.Username)
	if err != nil {
		ub.handleError(ctx, msg.Chat.ID, err)
		return
	}

	_, param, _ := strings.Cut(msg.Text, " ")
	if token, isInvite := strings.CutPrefix(strings.TrimSpace(param), invitePrefix); isInvite && !ok {
		switch err := ub.auth.AcceptInvite(ctx, token, userID); {
		case errors.Is(err, auth.ErrInviteNotValid):
			ub.send(ctx, msg.Chat.ID, "This invite link does not work. It was used or it expired. Ask the admin for a new link.", nil)
			return
		case err != nil:
			ub.handleError(ctx, msg.Chat.ID, err)
			return
		}
		ok = true
	}

	if !ok {
		ub.send(ctx, msg.Chat.ID, noAccessMessage, nil)
		return
	}
	ub.send(ctx, msg.Chat.ID, welcomeMessage, ub.openButton("Open the app", "/"))
}

// linkHandler gets all other messages. A message with a link gets a button that
// opens the Mini App to choose the files of the link.
func (ub *UndercastBot) linkHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil {
		return
	}
	userID := strconv.FormatInt(msg.From.ID, 10)

	url := findLink(msg.Text)
	if url == "" {
		ub.send(ctx, msg.Chat.ID, helpMessage, ub.openButton("Open the app", "/"))
		return
	}

	valid, err := ub.service.IsValidURL(ctx, url)
	if err != nil {
		ub.handleError(ctx, msg.Chat.ID, fmt.Errorf("failed to check url: %w", err))
		return
	}
	if !valid {
		ub.send(ctx, msg.Chat.ID, "I cannot use this link. Send a magnet link or a link to a video.", nil)
		return
	}

	link, err := ub.service.SaveSourceLink(ctx, userID, url)
	if err != nil {
		ub.handleError(ctx, msg.Chat.ID, err)
		return
	}
	if ub.cfg.Prefetch != nil {
		ub.cfg.Prefetch(ctx, link.URL)
	}

	text := "Choose the files and the feeds for the new episodes."
	if title := service.TitleFromSourceURL(url); title != "" {
		text = "<b>" + html.EscapeString(title) + "</b>\n\n" + text
	}
	ub.send(ctx, msg.Chat.ID, text, ub.openButton("Choose files", "/new/"+link.ID))
}

// findLink returns the first magnet link or web link in the text, or "".
func findLink(text string) string {
	for _, word := range strings.Fields(text) {
		lower := strings.ToLower(word)
		if strings.HasPrefix(lower, "magnet:") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") {
			return word
		}
	}
	return ""
}
