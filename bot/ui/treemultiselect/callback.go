package treemultiselect

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"golang.org/x/exp/maps"
)

const (
	cmdSelectNode = iota
	cmdSelectByFilter
	cmdGotoPage
	cmdUp
	cmdAction
	cmdNop
)

func (tms *TreeMultiSelect) callback(ctx context.Context, b *bot.Bot, update *models.Update) {
	st := tms.decodeState(update.CallbackQuery.Data)

	switch st.cmd {
	case cmdSelectNode:
		tms.selectNode(ctx, b, update.CallbackQuery.Message, st.param)
	case cmdSelectByFilter:
		tms.selectByFilter(ctx, b, update.CallbackQuery.Message, st.param)
	case cmdGotoPage:
		tms.gotoPage(ctx, b, update.CallbackQuery.Message, st.param)
	case cmdUp:
		tms.goUp(ctx, b, update.CallbackQuery.Message, st.param)
	case cmdAction:
		tms.onAction(ctx, b, update, st.param)
	case cmdNop:
		// do nothing
	default:
		tms.onError(fmt.Errorf("unknown command: %d", st.cmd))
	}

	tms.callbackAnswer(ctx, b, update.CallbackQuery)
}

func (tms *TreeMultiSelect) selectNode(ctx context.Context, b *bot.Bot, mes *models.Message, id int) {
	node := tms.nodeMap[id]

	func() {
		tms.nodesLock.Lock()
		defer tms.nodesLock.Unlock()

		if node.IsLeaf() {
			node.Selected = !node.Selected
			if tms.dynamicActionButtons != nil {
				tms.actionButtons = tms.dynamicActionButtons(tms.getAllSelectedNodes())
			}
		} else {
			tms.currentNode = node
			if tms.dynamicFilterButtons != nil {
				tms.filterButtons = tms.dynamicFilterButtons(maps.Values(tms.currentNode.Children))
			}
			tms.prevPages = append(tms.prevPages, tms.currentPage)
			tms.currentPage = 0
		}
	}()

	tms.sendUpdatedMarkup(ctx, b, mes)
}

func (tms *TreeMultiSelect) sendUpdatedMarkup(ctx context.Context, b *bot.Bot, mes *models.Message) {
	tms.nodesLock.RLock()
	defer tms.nodesLock.RUnlock()

	_, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID:      mes.Chat.ID,
		MessageID:   mes.ID,
		ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: tms.buildKeyboard()},
	})
	if err != nil {
		tms.onError(fmt.Errorf("error edit message in selectNode, %w", err))
	}
}

func (tms *TreeMultiSelect) callbackAnswer(ctx context.Context, b *bot.Bot, callbackQuery *models.CallbackQuery) {
	ok, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQuery.ID,
	})
	if err != nil {
		tms.onError(err)
		return
	}
	if !ok {
		tms.onError(fmt.Errorf("callback answer failed"))
	}
}

func (tms *TreeMultiSelect) deleteMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	_, errDelete := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    update.CallbackQuery.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.ID,
	})
	if errDelete != nil {
		tms.onError(fmt.Errorf("failed to delete message: %w", errDelete))
	}
	b.UnregisterHandler(tms.callbackHandlerID)
}
