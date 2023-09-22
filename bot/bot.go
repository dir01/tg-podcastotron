package bot

import (
	"context"
	"fmt"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/hori-ryota/zaperr"
	"go.uber.org/zap"
	"tg-podcastotron/auth"
	"tg-podcastotron/service"
)

func NewUndercastBot(
	token string,
	auth *auth.Service,
	repository Repository,
	service *service.Service,
	logger *zap.Logger,
) *UndercastBot {
	return &UndercastBot{
		logger:     logger,
		token:      token,
		auth:       auth,
		service:    service,
		repository: repository,
	}
}

type Repository interface {
	SetChatID(ctx context.Context, userID string, chatID int64) error
	GetChatID(ctx context.Context, userID string) (int64, error)
}

type UndercastBot struct {
	logger     *zap.Logger
	token      string
	bot        *bot.Bot
	auth       *auth.Service
	service    *service.Service
	repository Repository

	episodesStatusChangesChan chan []service.EpisodeStatusChange
}

func (ub *UndercastBot) Start(ctx context.Context) error {
	opts := []bot.Option{
		bot.WithDefaultHandler(ub.urlHandler),
		bot.WithMiddlewares(ub.authenticate, ub.setMenuMiddleware),
	}

	ub.episodesStatusChangesChan = ub.service.Start(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case statusChanges := <-ub.episodesStatusChangesChan:
				ub.onEpisodesStatusChanges(ctx, statusChanges)
			}
		}
	}()

	go ub.pollExpiredEpisodes(ctx, time.NewTicker(24*time.Hour), 30*24*time.Hour)

	var err error
	ub.bot, err = bot.New(ub.token, opts...)
	if err != nil {
		return zaperr.Wrap(err, "error while creating go-telegram/bot")
	}

	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, ub.helpHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, ub.helpHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/ep", bot.MatchTypePrefix, ub.listEpisodesHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/ee", bot.MatchTypePrefix, ub.editEpisodesHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/f", bot.MatchTypePrefix, ub.listFeedsHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/ef", bot.MatchTypePrefix, ub.editFeedsHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/nf", bot.MatchTypeExact, ub.newFeedHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/adduser", bot.MatchTypeExact, ub.addUserHandler)
	ub.bot.RegisterHandlerMatchFunc(func(update *models.Update) bool {
		return update != nil && update.Message != nil && update.Message.Contact != nil
	}, ub.addUserHandler)
	ub.bot.Start(ctx)

	return nil
}

func (ub *UndercastBot) pollExpiredEpisodes(
	ctx context.Context,
	pollingTicker *time.Ticker,
	epExpirationAge time.Duration,
) {
	ub.logger.Info("starting expired episodes poller")
	for {
		select {
		case <-ctx.Done():
			return
		case <-pollingTicker.C:
			ub.logger.Info("listing expired episodes")
			expiredEps, err := ub.service.ListExpiredEpisodes(ctx, epExpirationAge)
			if err != nil {
				ub.logger.Error("error while listing expired episodes", zaperr.ToField(err))
				continue
			}

			for _, ep := range expiredEps {
				if err := ub.service.DeleteEpisodes(ctx, ep.UserID, []string{ep.ID}); err != nil {
					ub.logger.Error("error while deleting episode", zaperr.ToField(err))
				} else {
					ub.logger.Info(
						"deleted episode",
						zap.String("id", ep.ID),
						zap.String("title", ep.Title),
						zap.String("url", ep.URL),
					)
				}
			}
		}
	}
}

func (ub *UndercastBot) handleError(ctx context.Context, chatID int64, err error) {
	id := uuid.New().String()
	ub.logger.Error("error", zap.String("id", id), zaperr.ToField(err))
	ub.sendTextMessage(ctx, chatID, "An error occurred while processing your request. Please try again later. \nError ID: %s", id)
}

func (ub *UndercastBot) sendTextMessage(ctx context.Context, chatID int64, message string, args ...interface{}) {
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf(message, args...),
	}); err != nil {
		ub.logger.Error("sendTextMessage error", zaperr.ToField(err))
	}
}
