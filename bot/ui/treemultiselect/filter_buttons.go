package treemultiselect

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type FilterButton struct {
	Text string
	Fn   func(node *TreeNode) bool
}

var FilterButtonSelectAll = FilterButton{Text: "Select All", Fn: func(item *TreeNode) bool { return true }}
var FilterButtonSelectNone = FilterButton{Text: "Select None", Fn: func(item *TreeNode) bool { return false }}

func (tms *TreeMultiSelect) selectByFilter(ctx context.Context, b *bot.Bot, message *models.Message, idx int) {
	filterBtn := tms.filterButtons[idx]

	func() {
		tms.nodesLock.Lock()
		defer tms.nodesLock.Unlock()

		for _, node := range tms.currentNode.Children {
			node.Selected = filterBtn.Fn(node)
		}
	}()

	if tms.dynamicActionButtons != nil {
		tms.actionButtons = tms.dynamicActionButtons(tms.getAllSelectedNodes())
	}

	tms.sendUpdatedMarkup(ctx, b, message)
}

func (tms *TreeMultiSelect) buildFiltersRow() []models.InlineKeyboardButton {
	if len(tms.filterButtons) == 0 {
		return nil
	}
	filterBtns := make([]models.InlineKeyboardButton, 0, len(tms.filterButtons))
	for idx, filter := range tms.filterButtons {
		filterBtns = append(filterBtns, models.InlineKeyboardButton{
			Text:         filter.Text,
			CallbackData: tms.encodeState(state{cmd: cmdSelectByFilter, param: idx}),
		})
	}
	return filterBtns
}
