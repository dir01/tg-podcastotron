package bot

import (
	"reflect"
	"testing"

	"tg-podcastotron/service"
)

func TestFinishedMessage(t *testing.T) {
	feeds := []*service.Feed{{ID: "1", Title: "Main"}, {ID: "2", Title: "Books & <stuff>"}, {ID: "3", Title: "Unused"}}
	ep := func(status service.EpisodeStatus, feedIDs ...string) *service.Episode {
		return &service.Episode{Status: status, FeedIDs: feedIDs, Title: "Chapter"}
	}
	const src = "magnet:?xt=urn:btih:abc&dn=The+Book"

	cases := map[string]struct {
		episodes []*service.Episode
		want     string
	}{
		"one ready": {
			[]*service.Episode{ep(service.EpisodeStatusComplete, "1")},
			"✅ <b>Chapter</b>\nThe episode is ready in <b>Main</b>.",
		},
		"one failed": {
			[]*service.Episode{ep(service.EpisodeStatusFailed, "1")},
			"❌ <b>Chapter</b>\nThe episode failed.",
		},
		"all ready in two feeds": {
			[]*service.Episode{ep(service.EpisodeStatusComplete, "1", "2"), ep(service.EpisodeStatusComplete, "2")},
			"✅ <b>The Book</b>\n2 episodes are ready in <b>Main</b> and <b>Books &amp; &lt;stuff&gt;</b>.",
		},
		"some failed": {
			[]*service.Episode{ep(service.EpisodeStatusComplete, "2"), ep(service.EpisodeStatusFailed, "1"), ep(service.EpisodeStatusComplete, "2")},
			"⚠️ <b>The Book</b>\n2 of 3 episodes are ready in <b>Books &amp; &lt;stuff&gt;</b>. 1 failed.",
		},
		"all failed": {
			[]*service.Episode{ep(service.EpisodeStatusFailed, "1"), ep(service.EpisodeStatusFailed, "1")},
			"❌ <b>The Book</b>\nAll 2 episodes failed.",
		},
		"no feed": {
			[]*service.Episode{ep(service.EpisodeStatusComplete), ep(service.EpisodeStatusComplete)},
			"✅ <b>The Book</b>\n2 episodes are ready.",
		},
	}
	for name, tc := range cases {
		req := &service.Request{SourceURL: src, Episodes: tc.episodes}
		if got := finishedMessage(req, feeds); got != tc.want {
			t.Errorf("%s:\ngot  %q\nwant %q", name, got, tc.want)
		}
	}
}

func TestCopyFeedLinkButtons(t *testing.T) {
	texts := func(feeds ...*service.Feed) []string {
		var got []string
		for _, row := range copyFeedLinkButtons(feeds) {
			got = append(got, row[0].Text+" = "+row[0].CopyText.Text)
		}
		return got
	}
	main := &service.Feed{Title: "Main", URL: "https://s3.example.com/feeds/u/1"}
	books := &service.Feed{Title: "Books", URL: "https://s3.example.com/feeds/u/2"}
	noURL := &service.Feed{Title: "No URL"}

	cases := map[string]struct {
		got, want []string
	}{
		"no feeds": {texts(), nil},
		"one feed": {texts(main), []string{"Copy the feed link = https://s3.example.com/feeds/u/1"}},
		"two feeds": {texts(main, books), []string{
			"Copy the link: Main = https://s3.example.com/feeds/u/1",
			"Copy the link: Books = https://s3.example.com/feeds/u/2",
		}},
		"feed without a URL": {texts(noURL), nil},
		"too many feeds":     {texts(main, books, main, books)[maxCopyButtons:], []string{}},
	}
	for name, tc := range cases {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s:\ngot  %q\nwant %q", name, tc.got, tc.want)
		}
	}
}

func TestFindLink(t *testing.T) {
	cases := map[string]string{
		"magnet:?xt=urn:btih:abc":                     "magnet:?xt=urn:btih:abc",
		"look at https://youtu.be/x please":           "https://youtu.be/x",
		"HTTP://EXAMPLE.COM/a":                        "HTTP://EXAMPLE.COM/a",
		"no link here":                                "",
		"first magnet:?xt=1 then https://example.com": "magnet:?xt=1",
	}
	for text, want := range cases {
		if got := findLink(text); got != want {
			t.Errorf("findLink(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestNotifiedRequests(t *testing.T) {
	var n notifiedRequests
	if !n.add("u/r") {
		t.Fatal("first add = false")
	}
	if n.add("u/r") {
		t.Fatal("second add = true")
	}
	if !n.add("u/other") {
		t.Fatal("add of other key = false")
	}
}

func TestIsStartCommand(t *testing.T) {
	for text, want := range map[string]bool{
		"/start":          true,
		"/start inv_abc":  true,
		"/startle":        false,
		"magnet:?/start ": false,
	} {
		if got := isStartCommand(text); got != want {
			t.Errorf("isStartCommand(%q) = %v, want %v", text, got, want)
		}
	}
}
