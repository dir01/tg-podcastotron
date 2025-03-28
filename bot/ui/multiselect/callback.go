package multiselect

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	cmdSelectItem = iota
	cmdSelectByFilter
	cmdGotoPage
	cmdNop
	cmdAction
)

func (ms *MultiSelect) callbackAnswer(ctx context.Context, b *bot.Bot, callbackQuery *models.CallbackQuery) {
	ok, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQuery.ID,
	})
	if err != nil {
		ms.onError(err)
		return
	}
	if !ok {
		ms.onError(fmt.Errorf("callback answer failed"))
	}
}

func (ms *MultiSelect) callback(ctx context.Context, b *bot.Bot, update *models.Update) {
	st := ms.decodeState(update.CallbackQuery.Data)

	switch st.cmd {
	case cmdSelectItem:
		ms.selectItem(ctx, b, update.CallbackQuery.Message.Message, st.param)
	case cmdSelectByFilter:
		ms.selectByFilter(ctx, b, update.CallbackQuery.Message.Message, st.param)
	case cmdGotoPage:
		ms.gotoPage(ctx, b, update.CallbackQuery.Message.Message, st.param)
	case cmdAction:
		ms.onAction(ctx, b, update, st.param)
	case cmdNop:
		// do nothing
	default:
		ms.onError(fmt.Errorf("unknown command: %d", st.cmd))
	}

	ms.callbackAnswer(ctx, b, update.CallbackQuery)
}

func (ms *MultiSelect) deleteMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	_, errDelete := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
	})
	if errDelete != nil {
		ms.onError(fmt.Errorf("failed to delete message: %w", errDelete))
	}
	b.UnregisterHandler(ms.callbackHandlerID)
}

func (ms *MultiSelect) selectItem(ctx context.Context, b *bot.Bot, mes *models.Message, itemID string) {
	func() {
		ms.itemsLock.Lock()
		defer ms.itemsLock.Unlock()

		if ms.onItemSelectedHandler == nil {
			item, ok := ms.itemsMap[itemID]
			if !ok {
				ms.onError(fmt.Errorf("item not found: %s", itemID))
				return
			}
			item.Selected = !item.Selected
		} else {
			stateChange := ms.onItemSelectedHandler(itemID)
			if stateChange == nil {
				return
			}
			if stateChange.Items != nil {
				ms.items = stateChange.Items
			}
			if stateChange.CurrentPage != nil {
				ms.currentPage = *stateChange.CurrentPage
			}
			if stateChange.ItemFilters != nil {
				ms.itemFilters = stateChange.ItemFilters
			}
			if stateChange.ActionButtons != nil {
				ms.actionButtons = stateChange.ActionButtons
			}
		}
	}()

	ms.sendUpdatedMarkup(ctx, b, mes)
}

func (ms *MultiSelect) gotoPage(ctx context.Context, b *bot.Bot, mes *models.Message, strPage string) {
	page, err := strconv.Atoi(strPage)
	if err != nil {
		ms.onError(fmt.Errorf("gotoPage failed to parse strPage %s, %w", strPage, err))
		return
	}
	if page < 0 || page >= ms.pagesCount() {
		ms.onError(fmt.Errorf("gotoPage invalid page: %d", page))
		return
	}
	ms.currentPage = page
	ms.sendUpdatedMarkup(ctx, b, mes)
}

func (ms *MultiSelect) selectByFilter(ctx context.Context, b *bot.Bot, message *models.Message, strIdx string) {
	idx, err := strconv.Atoi(strIdx)
	if err != nil {
		ms.onError(fmt.Errorf("selectByFilter failed to parse strIdx %s, %w", strIdx, err))
		return
	}

	filter := ms.itemFilters[idx]

	func() {
		ms.itemsLock.Lock()
		defer ms.itemsLock.Unlock()

		for idx := range ms.items {
			ms.items[idx].Selected = filter.Fn(ms.items[idx])
		}
	}()

	ms.sendUpdatedMarkup(ctx, b, message)
}

func (ms *MultiSelect) sendUpdatedMarkup(ctx context.Context, b *bot.Bot, mes *models.Message) {
	_, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID:      mes.Chat.ID,
		MessageID:   mes.ID,
		ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: ms.buildKeyboard()},
	})
	if err != nil {
		ms.onError(fmt.Errorf("error edit message in selectItem, %w", err))
	}
}
