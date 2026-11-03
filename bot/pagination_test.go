package bot

import (
	"fmt"
	"strings"
	"testing"

	"tg-podcastotron/service"
)

func TestPaginateLines(t *testing.T) {
	cases := []struct {
		lines  []string
		maxLen int
		want   [][]string
	}{
		{nil, 10, nil},
		{[]string{"aaa", "bbb", "ccc"}, 7, [][]string{{"aaa", "bbb"}, {"ccc"}}},
		{[]string{"aaa", "bbb", "ccc"}, 6, [][]string{{"aaa"}, {"bbb"}, {"ccc"}}},
		{[]string{"aaaaaaaaaaaa", "b"}, 5, [][]string{{"aaaaaaaaaaaa"}, {"b"}}},
	}
	for _, tc := range cases {
		got := paginateLines(tc.lines, tc.maxLen)
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("paginateLines(%v, %d) = %v, want %v", tc.lines, tc.maxLen, got, tc.want)
		}
	}
}

func TestRenderEpisodesPage(t *testing.T) {
	ub := &UndercastBot{}

	if text, markup := ub.renderEpisodesPage(nil, 0); text != "You have no episodes yet" || markup != nil {
		t.Errorf("empty list: got %q, %+v", text, markup)
	}

	eps := make([]*service.Episode, 300)
	for i := range eps {
		eps[i] = &service.Episode{ID: fmt.Sprint(i + 1), Title: strings.Repeat("Long title ", 5)}
	}
	text, markup := ub.renderEpisodesPage(eps, 1)
	if !fitsTelegram(text) {
		t.Fatalf("page is too long: %d bytes", len(text))
	}
	if !strings.HasPrefix(text, "Episodes: 300\n") || !strings.Contains(text, "Page 2 of ") {
		t.Errorf("missing header or page number in:\n%s", text)
	}
	if markup == nil || len(markup.InlineKeyboard[0]) != 2 ||
		markup.InlineKeyboard[0][0].CallbackData != "epp:0" || markup.InlineKeyboard[0][1].CallbackData != "epp:2" {
		t.Errorf("second page must have the previous and the next buttons, got %+v", markup)
	}
}
