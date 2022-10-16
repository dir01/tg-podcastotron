package bot

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"undercast-bot/auth"
	"undercast-bot/bot/ui/multiselect"
	"undercast-bot/bot/ui/treemultiselect"
	"undercast-bot/mediary"
)

func NewUndercastBot(token string, auth *auth.Service, mediary *mediary.Service) *UndercastBot {
	return &UndercastBot{
		token:   token,
		auth:    auth,
		mediary: mediary,
	}
}

type UndercastBot struct {
	token   string
	bot     *bot.Bot
	auth    *auth.Service
	mediary *mediary.Service
}

func (ub *UndercastBot) Start(ctx context.Context) {
	opts := []bot.Option{
		bot.WithDefaultHandler(ub.urlHandler),
		bot.WithMiddlewares(ub.authenticate, ub.setMenuMiddleware),
	}

	ub.bot = bot.New(ub.token, opts...)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, ub.helpHandler)
	ub.bot.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, ub.helpHandler)
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

func (ub *UndercastBot) urlHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	url := update.Message.Text
	isValid, err := ub.mediary.IsValidURL(ctx, url)
	if err != nil {
		ub.respondError(ctx, update.Message.Chat.ID)
		return
	}
	if !isValid {
		ub.sendTextMessage(ctx, update.Message.Chat.ID, "Invalid URL")
		return
	}

	cancelChan := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-cancelChan:
			return
		case <-time.After(2 * time.Second):
			ub.sendTextMessage(ctx, update.Message.Chat.ID, "Fetching metadata, please wait...")
		}
	}()
	//close(cancelChan) // FIXME

	metadata, err := ub.mediary.FetchMetadata(ctx, url)
	close(cancelChan)
	if err != nil {
		ub.respondError(ctx, update.Message.Chat.ID)
		return
	}

	var paths []string
	for _, file := range metadata.Files {
		paths = append(paths, file.Path)
	}

	kb := treemultiselect.New(
		ub.bot,
		paths,
		nil, // onConfirmSelection is not needed if WithDynamicActionButtons is set
		treemultiselect.WithMaxNodesPerPage(10),
		treemultiselect.WithDynamicFilterButtons(func(selectedNodes []*treemultiselect.TreeNode) []treemultiselect.FilterButton {
			extCounter := make(map[string]int)
			for _, n := range selectedNodes {
				extCounter[filepath.Ext(n.Value)]++
			}
			delete(extCounter, "") // no-extension files

			topExt := ""
			topExtCount := 0
			for ext, count := range extCounter {
				if count > topExtCount {
					topExt = ext
					topExtCount = count
				}
			}

			if topExt != "" {
				return []treemultiselect.FilterButton{
					{
						Text: "Select *" + topExt,
						Fn: func(node *treemultiselect.TreeNode) bool {
							return strings.HasSuffix(node.Value, topExt)
						},
					},
					treemultiselect.FilterButtonSelectNone,
				}
			}

			return []treemultiselect.FilterButton{}
		}),
		treemultiselect.WithDynamicActionButtons(func(selectedNodes []*treemultiselect.TreeNode) []treemultiselect.ActionButton {
			switch len(selectedNodes) {
			case 0:
				return []treemultiselect.ActionButton{
					treemultiselect.NewCancelButton("Cancel", func(ctx context.Context, bot *bot.Bot, mes *models.Message) {

					}),
				}
			case 1:
				return []treemultiselect.ActionButton{
					treemultiselect.NewConfirmButton(
						"Create Episode",
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {

						}),
					treemultiselect.NewCancelButton("Cancel", func(ctx context.Context, bot *bot.Bot, mes *models.Message) {

					}),
				}
			default:
				return []treemultiselect.ActionButton{
					treemultiselect.NewConfirmButton(
						"1 File - 1 Episode",
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {

						}),
					treemultiselect.NewConfirmButton(
						fmt.Sprintf("%d Files - 1 Episode", len(selectedNodes)),
						func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string) {

						}),
					treemultiselect.NewCancelButton("Cancel", func(ctx context.Context, bot *bot.Bot, mes *models.Message) {

					}),
				}
			}
		}),
	)
	msg, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        fmt.Sprintf("Please choose which files to include in the episode"),
		ReplyMarkup: kb,
	})

	log.Printf("urlHandler: msg: %v, err: %v\n", msg, err)
}

func (ub *UndercastBot) onConfirmSelection(ctx context.Context, bot *bot.Bot, mes *models.Message, items []*multiselect.Item) {
	log.Printf("onConfirmSelection: items: %v+\n", items)
}

func (ub *UndercastBot) respondError(ctx context.Context, chatID int) {
	ub.sendTextMessage(ctx, chatID, "Service Temporarily Unavailable")
}

func (ub *UndercastBot) sendTextMessage(ctx context.Context, chatID int, message string, args ...interface{}) {
	if _, err := ub.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf(message, args...),
	}); err != nil {
		log.Printf("sendTextMessage err: %v\n", err)
	}
}
