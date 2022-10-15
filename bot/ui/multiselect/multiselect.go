package multiselect

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type OnConfirmSelectionHandler func(ctx context.Context, bot *bot.Bot, mes *models.Message, items []*Item)
type OnItemSelectedHandler func(itemID string) *StateChange
type OnCancelHandler func(ctx context.Context, bot *bot.Bot, mes *models.Message)
type OnErrorHandler func(err error)

type Item struct {
	Text     string
	Selected bool
	ID       string
}

type ItemFilter struct {
	Text string
	Fn   func(item *Item) bool
}

type MultiSelect struct {
	// configurable params
	formatItem            func(*Item) string
	deleteOnConfirmed     bool
	deleteOnCancel        bool
	maxItemsPerPage       int
	onItemSelectedHandler OnItemSelectedHandler
	onError               OnErrorHandler
	itemFilters           []ItemFilter
	actionButtons         []ActionButton

	// data
	items []*Item

	// internal
	prefix            string
	callbackHandlerID string
	currentPage       int
	itemsLock         sync.RWMutex
}

func New(b *bot.Bot, items []*Item, onConfirmSelection OnConfirmSelectionHandler, opts ...Option) *MultiSelect {
	for idx, item := range items {
		if item.ID == "" {
			item.ID = strconv.Itoa(idx)
			items[idx] = item
		}
	}
	multiSelect := &MultiSelect{
		formatItem: func(item *Item) string {
			if item.Selected {
				return "☑️ " + item.Text
			} else {
				return item.Text
			}
		},
		itemFilters: []ItemFilter{ItemFilterSelectAll, ItemFilterSelectNone},
		actionButtons: []ActionButton{
			NewCancelButton("Cancel", defaultOnCancel),
			NewConfirmButton("Confirm", onConfirmSelection),
		},
		deleteOnConfirmed:     true,
		deleteOnCancel:        true,
		maxItemsPerPage:       10,
		onItemSelectedHandler: nil,
		onError:               defaultOnError,
		items:                 items,
		prefix:                bot.RandomString(16),
	}

	for _, opt := range opts {
		opt(multiSelect)
	}

	multiSelect.callbackHandlerID = b.RegisterHandler(bot.HandlerTypeCallbackQueryData, multiSelect.prefix, bot.MatchTypePrefix, multiSelect.callback)

	return multiSelect
}

func (ms *MultiSelect) MarshalJSON() ([]byte, error) {
	return json.Marshal(&models.InlineKeyboardMarkup{InlineKeyboard: ms.buildKeyboard()})
}

func (ms *MultiSelect) onAction(ctx context.Context, b *bot.Bot, update *models.Update, strIdx string) {
	idx, err := strconv.Atoi(strIdx)
	if err != nil {
		ms.onError(err)
		return
	}
	action := ms.actionButtons[idx]
	switch action.Type {
	case actionTypeCancel:
		action.FnCancel(ctx, b, update.CallbackQuery.Message)
		if ms.deleteOnCancel {
			ms.deleteMessage(ctx, b, update)
		}
	case actionTypeConfirm:
		action.FnConfirm(ctx, b, update.CallbackQuery.Message, ms.items)
		if ms.deleteOnConfirmed {
			ms.deleteMessage(ctx, b, update)
		}
	}
	if action.Type == actionTypeCancel {
		action.FnCancel(ctx, b, update.CallbackQuery.Message)
		if ms.deleteOnCancel {
			ms.deleteMessage(ctx, b, update)
		}
	}
}

var ItemFilterSelectAll = ItemFilter{Text: "Select All", Fn: func(item *Item) bool { return true }}
var ItemFilterSelectNone = ItemFilter{Text: "Select None", Fn: func(item *Item) bool { return false }}

type actionType int

const (
	actionTypeCancel  = 0
	actionTypeConfirm = 1
)

type ActionButton struct {
	Text      string
	Type      actionType
	FnCancel  OnCancelHandler
	FnConfirm OnConfirmSelectionHandler
}

func NewCancelButton(text string, fn OnCancelHandler) ActionButton {
	return ActionButton{
		Text:     text,
		Type:     actionTypeCancel,
		FnCancel: fn,
	}
}

func NewConfirmButton(text string, fn OnConfirmSelectionHandler) ActionButton {
	return ActionButton{
		Text:      text,
		Type:      actionTypeConfirm,
		FnConfirm: fn,
	}
}

func defaultOnError(err error) {
	log.Printf("[TG-UI-MULTISELECT] [ERROR] %s", err)
}

func defaultOnCancel(_ context.Context, _ *bot.Bot, _ *models.Message) {}
