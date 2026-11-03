package bot

import (
	"fmt"
	"strings"

	"github.com/go-telegram/bot/models"
)

// pageMaxLen is the length budget of the lines on one page. The header and the
// page number need the rest of the 4096 characters of a Telegram message.
const pageMaxLen = 3000

// renderPagedLines renders the header and one page of the lines, and the buttons to
// go to the previous and the next page. pageCallback returns the callback data of the
// button for a page. A page that is out of range (for example, after items were
// deleted) shows the nearest page. The markup is nil if all lines fit on one page.
func renderPagedLines(header string, lines []string, page int, pageCallback func(page int) string) (string, *models.InlineKeyboardMarkup) {
	pages := paginateLines(lines, pageMaxLen)
	if len(pages) == 0 {
		return header, nil
	}
	page = max(0, min(page, len(pages)-1))

	text := header + "\n" + strings.Join(pages[page], "\n")
	if len(pages) == 1 {
		return text, nil
	}
	text += fmt.Sprintf("\n\nPage %d of %d", page+1, len(pages))

	var buttons []models.InlineKeyboardButton
	if page > 0 {
		buttons = append(buttons, models.InlineKeyboardButton{Text: "◀", CallbackData: pageCallback(page - 1)})
	}
	if page < len(pages)-1 {
		buttons = append(buttons, models.InlineKeyboardButton{Text: "▶", CallbackData: pageCallback(page + 1)})
	}
	return text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{buttons}}
}

// paginateLines splits lines into pages. Each page is at most maxLen bytes when its lines
// are joined with "\n". A line that is longer than maxLen gets a page of its own.
func paginateLines(lines []string, maxLen int) [][]string {
	var pages [][]string
	var cur []string
	curLen := 0
	for _, l := range lines {
		add := len(l)
		if len(cur) > 0 {
			add++ // the "\n" separator
		}
		if len(cur) > 0 && curLen+add > maxLen {
			pages = append(pages, cur)
			cur, curLen, add = nil, 0, len(l)
		}
		cur = append(cur, l)
		curLen += add
	}
	if len(cur) > 0 {
		pages = append(pages, cur)
	}
	return pages
}
