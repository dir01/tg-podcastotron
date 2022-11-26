package bot

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/google/uuid"
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

	episodesChan chan []*service.Episode
}

func (ub *UndercastBot) Start(ctx context.Context) {
	opts := []bot.Option{
		bot.WithDefaultHandler(ub.urlHandler),
		bot.WithMiddlewares(ub.authenticate, ub.setMenuMiddleware),
	}

	ub.episodesChan = ub.service.Start(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case episodes := <-ub.episodesChan:
				ub.onEpisodesCreated(ctx, episodes)
			}
		}
	}()

	ub.bot = bot.New(ub.token, opts...)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, ub.helpHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, ub.helpHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/episodes", bot.MatchTypeExact, ub.episodesHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/feeds", bot.MatchTypeExact, ub.feedsHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/unpublish_ep", bot.MatchTypePrefix, ub.unpublishEpisodesHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/publish_ep", bot.MatchTypePrefix, ub.publishEpisodesHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/playground", bot.MatchTypeExact, ub.playgroundHandler)
	ub.bot.Start(ctx)
}

type Metadata struct {
	Name  string         `json:"name"`
	Files []FileMetadata `json:"files"`
}

type FileMetadata struct {
	Path     string `json:"path"`
	LenBytes int64  `json:"length_bytes"`
}

func (ub *UndercastBot) handleError(ctx context.Context, chatID int, err error) {
	id := uuid.New().String()
	ub.logger.Error("error", zap.String("id", id), zap.Error(err))
	ub.sendTextMessage(ctx, chatID, "An error occurred while processing your request. Please try again later. \nError ID: %s", id)
}

func (ub *UndercastBot) sendTextMessage(ctx context.Context, chatID int, message string, args ...interface{}) {
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf(message, args...),
	}); err != nil {
		ub.logger.Error("sendTextMessage error", zap.Error(err))
	}
}
