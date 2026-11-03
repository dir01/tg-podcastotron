package bot

import (
	"fmt"
	"strings"
	"testing"

	"tg-podcastotron/service"
)

func TestRenderFeedPage(t *testing.T) {
	ub := &UndercastBot{}
	feed := &service.Feed{ID: "1", Title: "Books & <Stuff>", URL: "https://x/feed.xml"}

	t.Run("one page has no buttons", func(t *testing.T) {
		eps := []*service.Episode{{ID: "1", Title: "One"}, {ID: "2", Title: "Two"}}
		text, markup := ub.renderFeedPage(feed, eps, 0)
		if markup != nil {
			t.Errorf("unexpected buttons")
		}
		for _, want := range []string{"Books &amp; &lt;Stuff&gt;", "Episodes: 2 [edit: /ee_1_2]", "/ep_1", "/ep_2"} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in:\n%s", want, text)
			}
		}
	})

	t.Run("many episodes are split into pages that fit", func(t *testing.T) {
		eps := make([]*service.Episode, 300)
		for i := range eps {
			eps[i] = &service.Episode{ID: fmt.Sprint(i + 1), Title: strings.Repeat("Long title ", 5)}
		}

		text, markup := ub.renderFeedPage(feed, eps, 0)
		if !fitsTelegram(text) {
			t.Fatalf("first page is too long: %d bytes", len(text))
		}
		if markup == nil || len(markup.InlineKeyboard[0]) != 1 || markup.InlineKeyboard[0][0].CallbackData != "fp:1:1" {
			t.Fatalf("first page must have only the next button, got %+v", markup)
		}
		if !strings.Contains(text, "Page 1 of ") {
			t.Errorf("missing page number in:\n%s", text)
		}

		// A page that is out of range shows the last page.
		text, markup = ub.renderFeedPage(feed, eps, 1000)
		if !fitsTelegram(text) || !strings.Contains(text, "/ep_300]") {
			t.Errorf("last page must show the last episode:\n%s", text)
		}
		if len(markup.InlineKeyboard[0]) != 1 || markup.InlineKeyboard[0][0].Text != "◀" {
			t.Errorf("last page must have only the previous button, got %+v", markup)
		}
	})
}
