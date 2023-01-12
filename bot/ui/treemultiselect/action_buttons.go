package treemultiselect

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type ActionButton struct {
	Text      string
	Type      actionType
	FnCancel  OnCancelHandler
	FnConfirm OnConfirmSelectionHandler
}
type actionType int

const (
	actionTypeCancel  = 0
	actionTypeConfirm = 1
)

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

func (tms *TreeMultiSelect) buildActionRows() [][]models.InlineKeyboardButton {
	if len(tms.actionButtons) == 0 {
		return nil
	}
	var actionBtns [][]models.InlineKeyboardButton
	i := 0
	for _, actionRow := range tms.actionButtons {
		keyboardRow := make([]models.InlineKeyboardButton, 0, len(actionRow))
		for _, action := range actionRow {
			keyboardRow = append(keyboardRow, models.InlineKeyboardButton{
				Text:         action.Text,
				CallbackData: tms.encodeState(state{cmd: cmdAction, param: i}),
			})
			i++
		}
		actionBtns = append(actionBtns, keyboardRow)
	}
	return actionBtns
}

func (tms *TreeMultiSelect) onAction(ctx context.Context, b *bot.Bot, update *models.Update, idx int) {
	var action ActionButton
outerLoop:
	for _, actionRow := range tms.actionButtons {
		for _, action = range actionRow {
			if idx == 0 {
				break outerLoop
			}
			idx--
		}
	}

	switch action.Type {
	case actionTypeCancel:
		action.FnCancel(ctx, b, update.CallbackQuery.Message)
		if tms.deleteOnCancel {
			tms.deleteMessage(ctx, b, update)
		}
	case actionTypeConfirm:
		action.FnConfirm(ctx, b, update.CallbackQuery.Message, tms.prepareResults())
		if tms.deleteOnConfirmed {
			tms.deleteMessage(ctx, b, update)
		}
	}
}
