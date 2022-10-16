package treemultiselect

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"golang.org/x/exp/maps"
)

func (tms *TreeMultiSelect) gotoPage(ctx context.Context, b *bot.Bot, mes *models.Message, page int) {
	if page < 0 || page >= tms.pagesCount() {
		tms.onError(fmt.Errorf("gotoPage invalid page: %d", page))
		return
	}
	tms.currentPage = page
	tms.sendUpdatedMarkup(ctx, b, mes)
}

func (tms *TreeMultiSelect) prepareNodesPage() []*TreeNode {
	nodes := maps.Values(tms.currentNode.Children)
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Value < nodes[j].Value
	})

	if len(tms.currentNode.Children) > tms.maxNodesPerPage {
		begin := tms.currentPage * tms.maxNodesPerPage
		end := (tms.currentPage + 1) * tms.maxNodesPerPage
		if end > len(nodes) {
			end = len(nodes)
		}
		nodes = nodes[begin:end]
	}
	return nodes
}

func (tms *TreeMultiSelect) pagesCount() int {
	maxPage := len(tms.currentNode.Children) / tms.maxNodesPerPage
	if len(tms.currentNode.Children)%tms.maxNodesPerPage != 0 {
		maxPage++
	}
	return maxPage
}

func (tms *TreeMultiSelect) buildPaginationRow() []models.InlineKeyboardButton {
	if len(tms.currentNode.Children) <= tms.maxNodesPerPage {
		return nil
	}

	maxPage := tms.pagesCount()

	emptyBtn := models.InlineKeyboardButton{
		Text:         " ",
		CallbackData: tms.encodeState(state{cmd: cmdNop}),
	}

	var row []models.InlineKeyboardButton

	if tms.currentPage > 0 {
		if tms.currentPage > 1 {
			row = append(row, models.InlineKeyboardButton{
				Text:         "⏮️",
				CallbackData: tms.encodeState(state{cmd: cmdGotoPage, param: 0}),
			})
		} else {
			row = append(row, emptyBtn)
		}
		row = append(row, models.InlineKeyboardButton{
			Text:         "◀️",
			CallbackData: tms.encodeState(state{cmd: cmdGotoPage, param: tms.currentPage - 1}),
		})

	} else {
		row = append(row, emptyBtn, emptyBtn)
	}

	row = append(row, models.InlineKeyboardButton{
		Text:         fmt.Sprintf("%d/%d", tms.currentPage+1, maxPage),
		CallbackData: tms.encodeState(state{cmd: cmdNop}),
	})

	if tms.currentPage < maxPage-1 {
		row = append(row, models.InlineKeyboardButton{
			Text:         "▶️",
			CallbackData: tms.encodeState(state{cmd: cmdGotoPage, param: tms.currentPage + 1}),
		})
		if tms.currentPage < maxPage-2 {
			row = append(row, models.InlineKeyboardButton{
				Text:         "⏭️",
				CallbackData: tms.encodeState(state{cmd: cmdGotoPage, param: maxPage - 1}),
			})
		} else {
			row = append(row, emptyBtn)
		}
	} else {
		row = append(row, emptyBtn, emptyBtn)
	}

	return row
}
