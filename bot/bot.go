package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/google/uuid"
	"github.com/hori-ryota/zaperr"
	"go.uber.org/zap"
	"undercast-bot/auth"
	"undercast-bot/service"
)

func NewUndercastBot(token string, auth *auth.Service, botStore *RedisStore, service *service.Service, logger *zap.Logger) *UndercastBot {
	return &UndercastBot{
		logger:  logger,
		token:   token,
		auth:    auth,
		service: service,
		store:   botStore,
	}
}

type UndercastBot struct {
	logger  *zap.Logger
	token   string
	bot     *bot.Bot
	auth    *auth.Service
	service *service.Service
	store   *RedisStore

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
	ub.bot.Start(ctx)

	return nil
}

type Metadata struct {
	Name  string         `json:"name"`
	Files []FileMetadata `json:"files"`
}

type FileMetadata struct {
	Path     string `json:"path"`
	LenBytes int64  `json:"length_bytes"`
}

func (ub *UndercastBot) handleError(ctx context.Context, chatID int64, err error) {
	id := uuid.New().String()
	ub.logger.Error("error", zap.String("id", id), zap.Error(err))
	ub.sendTextMessage(ctx, chatID, "An error occurred while processing your request. Please try again later. \nError ID: %s", id)
}

func (ub *UndercastBot) sendTextMessage(ctx context.Context, chatID int64, message string, args ...interface{}) {
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf(message, args...),
	}); err != nil {
		ub.logger.Error("sendTextMessage error", zap.Error(err))
	}
}
