package treemultiselect

import (
	"github.com/go-telegram/bot/models"
)

func (tms *TreeMultiSelect) buildKeyboard() [][]models.InlineKeyboardButton {
	data := make([][]models.InlineKeyboardButton, 0, len(tms.currentNode.Children)+1)

	data = append(data, tms.buildNodesRows()...)

	if filterButtons := tms.buildFiltersRow(); filterButtons != nil {
		data = append(data, filterButtons)
	}

	if paginationButtons := tms.buildPaginationRow(); paginationButtons != nil {
		data = append(data, paginationButtons)
	}

	if actionButtons := tms.buildActionsRow(); actionButtons != nil {
		data = append(data, actionButtons)
	}

	return data
}

func (tms *TreeMultiSelect) buildNodesRows() [][]models.InlineKeyboardButton {
	nodesPage := tms.prepareNodesPage()

	nodesRows := make([][]models.InlineKeyboardButton, 0, len(nodesPage))
	for _, itm := range nodesPage {
		nodesRows = append(nodesRows, []models.InlineKeyboardButton{{
			Text:         tms.formatNode(itm),
			CallbackData: tms.encodeState(state{cmd: cmdSelectNode, param: itm.ID}),
		}})
	}

	if !tms.currentNode.IsRoot() {
		prevPage := 0
		if len(tms.prevPages) > 0 {
			prevPage = tms.prevPages[len(tms.prevPages)-1]
		}
		nodesRows = append([][]models.InlineKeyboardButton{{{
			Text:         tms.formatUpBtn(tms.currentNode),
			CallbackData: tms.encodeState(state{cmd: cmdUp, param: prevPage}),
		}}}, nodesRows...)
	}

	return nodesRows
}
