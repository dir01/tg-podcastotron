// Package bot is the chat side of the app. The Mini App (package tgminiapp) has the
// UI. The bot only gets links, opens the Mini App, accepts invites, and tells the
// user when the episodes of a request are finished.
package bot

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tg-podcastotron/auth"
	"tg-podcastotron/service"
	"tg-podcastotron/telemetry"
)

type Config struct {
	Token string
	// MiniAppURL is the public HTTPS URL of the Mini App, without the last "/".
	MiniAppURL string
	// Prefetch, if set, starts to get the file list of a link, so that the Mini App shows it sooner.
	Prefetch func(ctx context.Context, url string)
}

type Repository interface {
	SetChatID(ctx context.Context, userID string, chatID int64) error
	GetChatID(ctx context.Context, userID string) (int64, error)
}

type UndercastBot struct {
	cfg        Config
	logger     *slog.Logger
	bot        *bot.Bot
	auth       *auth.Service
	service    *service.Service
	repository Repository
	notified   notifiedRequests
}

// NewUndercastBot connects to Telegram. It returns an error if the token is not correct.
func NewUndercastBot(
	cfg Config,
	auth *auth.Service,
	repository Repository,
	service *service.Service,
	logger *slog.Logger,
) (*UndercastBot, error) {
	ub := &UndercastBot{
		cfg:        cfg,
		logger:     logger,
		auth:       auth,
		service:    service,
		repository: repository,
	}
	b, err := bot.New(cfg.Token,
		bot.WithDefaultHandler(ub.linkHandler),
		bot.WithMiddlewares(ub.trace, ub.authenticate),
		bot.WithHTTPClient(telegramPollTimeout, newTelegramHTTPClient()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}
	ub.bot = b
	return ub, nil
}

// Username returns the username of the bot.
func (ub *UndercastBot) Username(ctx context.Context) (string, error) {
	me, err := ub.bot.GetMe(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get bot user: %w", err)
	}
	return me.Username, nil
}

// Start gets updates and status changes until ctx is done.
func (ub *UndercastBot) Start(ctx context.Context, statusChanges <-chan service.EpisodeStatusChanges) {
	ub.setUpMenu(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case batch := <-statusChanges:
				ub.handleEpisodesStatusChanges(ctx, batch)
			}
		}
	}()

	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypePrefix, ub.startHandler)
	ub.bot.Start(ctx)
}

// setUpMenu replaces the commands of the old chat UI with a menu button that opens the Mini App.
func (ub *UndercastBot) setUpMenu(ctx context.Context) {
	if _, err := ub.bot.DeleteMyCommands(ctx, &bot.DeleteMyCommandsParams{}); err != nil {
		ub.logger.WarnContext(ctx, "failed to delete bot commands", slog.Any("error", err))
	}
	if _, err := ub.bot.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{
		MenuButton: models.MenuButtonWebApp{
			Type:   models.MenuButtonTypeWebApp,
			Text:   "Open",
			WebApp: models.WebAppInfo{URL: ub.miniAppURL("/")},
		},
	}); err != nil {
		ub.logger.ErrorContext(ctx, "failed to set the menu button", slog.Any("error", err))
	}
}

func (ub *UndercastBot) miniAppURL(path string) string {
	return ub.cfg.MiniAppURL + path
}

// openButton is a keyboard with one button that opens the Mini App at path.
func (ub *UndercastBot) openButton(text, path string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: text, WebApp: &models.WebAppInfo{URL: ub.miniAppURL(path)}},
	}}}
}

func (ub *UndercastBot) send(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) {
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:             chatID,
		Text:               text,
		ParseMode:          models.ParseModeHTML,
		ReplyMarkup:        markup,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()},
	}); err != nil {
		ub.logger.ErrorContext(ctx, "failed to send message", slog.Int64("chat_id", chatID), slog.Any("error", err))
	}
}

func (ub *UndercastBot) handleError(ctx context.Context, chatID int64, err error) {
	telemetry.RecordError(spanFromContext(ctx), err)
	ub.logger.ErrorContext(ctx, "error", slog.Any("error", err))
	ub.send(ctx, chatID, "Something went wrong. Try again later.", nil)
}
