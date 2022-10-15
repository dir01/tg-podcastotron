package multiselect

import (
	"fmt"
	"strconv"

	"github.com/go-telegram/bot/models"
)

type StateChange struct {
	Items         []*Item
	ItemFilters   []ItemFilter
	ActionButtons []ActionButton
	CurrentPage   *int
}

func (ms *MultiSelect) buildKeyboard() [][]models.InlineKeyboardButton {
	data := make([][]models.InlineKeyboardButton, 0, len(ms.items)+1)

	data = append(data, ms.buildItemsRows()...)

	if filterButtons := ms.buildFiltersRow(); filterButtons != nil {
		data = append(data, filterButtons)
	}

	if paginationButtons := ms.buildPaginationRow(); paginationButtons != nil {
		data = append(data, paginationButtons)
	}

	if actionButtons := ms.buildActionsRow(); actionButtons != nil {
		data = append(data, actionButtons)
	}

	return data
}

func (ms *MultiSelect) buildItemsRows() [][]models.InlineKeyboardButton {
	var items []*Item

	if len(ms.items) <= ms.maxItemsPerPage {
		items = ms.items
	} else {
		begin := ms.currentPage * ms.maxItemsPerPage
		end := (ms.currentPage + 1) * ms.maxItemsPerPage
		if end > len(ms.items) {
			end = len(ms.items)
		}
		items = ms.items[begin:end]
	}

	itemsRows := make([][]models.InlineKeyboardButton, 0, len(items))
	for _, itm := range items {
		itemsRows = append(itemsRows, []models.InlineKeyboardButton{{
			Text:         ms.formatItem(itm),
			CallbackData: ms.encodeState(state{cmd: cmdSelectItem, param: itm.ID}),
		}})
	}

	return itemsRows
}

func (ms *MultiSelect) buildFiltersRow() []models.InlineKeyboardButton {
	if len(ms.itemFilters) == 0 {
		return nil
	}
	filterBtns := make([]models.InlineKeyboardButton, 0, len(ms.itemFilters))
	for idx, filter := range ms.itemFilters {
		filterBtns = append(filterBtns, models.InlineKeyboardButton{
			Text:         filter.Text,
			CallbackData: ms.encodeState(state{cmd: cmdSelectByFilter, param: strconv.Itoa(idx)}),
		})
	}
	return filterBtns
}

func (ms *MultiSelect) buildActionsRow() []models.InlineKeyboardButton {
	if len(ms.actionButtons) == 0 {
		return nil
	}
	var actionBtns []models.InlineKeyboardButton
	for idx, action := range ms.actionButtons {
		actionBtns = append(actionBtns, models.InlineKeyboardButton{
			Text:         action.Text,
			CallbackData: ms.encodeState(state{cmd: cmdAction, param: strconv.Itoa(idx)}),
		})
	}
	return actionBtns
}

func (ms *MultiSelect) buildPaginationRow() []models.InlineKeyboardButton {
	if len(ms.items) <= ms.maxItemsPerPage {
		return nil
	}

	maxPage := ms.pagesCount()

	emptyBtn := models.InlineKeyboardButton{
		Text:         " ",
		CallbackData: ms.encodeState(state{cmd: cmdNop}),
	}

	var row []models.InlineKeyboardButton

	if ms.currentPage > 0 {
		row = append(row, models.InlineKeyboardButton{
			Text:         "◀️",
			CallbackData: ms.encodeState(state{cmd: cmdGotoPage, param: strconv.Itoa(ms.currentPage - 1)}),
		})
		if ms.currentPage > 1 {
			row = append(row, models.InlineKeyboardButton{
				Text:         "⏮️",
				CallbackData: ms.encodeState(state{cmd: cmdGotoPage, param: "0"}),
			})
		} else {
			row = append(row, emptyBtn)
		}
	} else {
		row = append(row, emptyBtn, emptyBtn)
	}

	row = append(row, models.InlineKeyboardButton{
		Text:         fmt.Sprintf("%d/%d", ms.currentPage+1, maxPage),
		CallbackData: ms.encodeState(state{cmd: cmdNop}),
	})

	if ms.currentPage < maxPage-1 {
		row = append(row, models.InlineKeyboardButton{
			Text:         "▶️",
			CallbackData: ms.encodeState(state{cmd: cmdGotoPage, param: strconv.Itoa(ms.currentPage + 1)}),
		})
		if ms.currentPage < maxPage-2 {
			row = append(row, models.InlineKeyboardButton{
				Text:         "⏭️",
				CallbackData: ms.encodeState(state{cmd: cmdGotoPage, param: strconv.Itoa(maxPage - 1)}),
			})
		} else {
			row = append(row, emptyBtn)
		}
	} else {
		row = append(row, emptyBtn, emptyBtn)
	}

	return row
}

func (ms *MultiSelect) maybePaginateItems() []*Item {
	var items []*Item
	if len(ms.items) <= ms.maxItemsPerPage {
		items = ms.items
	} else {
		begin := ms.currentPage * ms.maxItemsPerPage
		end := (ms.currentPage + 1) * ms.maxItemsPerPage
		if end > len(ms.items) {
			end = len(ms.items)
		}
		items = ms.items[begin:end]
	}
	return items
}

func (ms *MultiSelect) pagesCount() int {
	maxPage := len(ms.items) / ms.maxItemsPerPage
	if len(ms.items)%ms.maxItemsPerPage != 0 {
		maxPage++
	}
	return maxPage
}
