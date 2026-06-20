package bot

import (
	"strings"
	"testing"
	"time"

	"tg-podcastotron/service"
)

func TestFormatElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		0:                "00:00:00",
		-5 * time.Second: "00:00:00",
		90 * time.Second: "00:01:30",
		(time.Hour + 2*time.Minute + 3*time.Second): "01:02:03",
		500 * time.Millisecond:                      "00:00:01", // rounds to nearest second
	}
	for in, want := range cases {
		if got := formatElapsed(in); got != want {
			t.Errorf("formatElapsed(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderEpisodeLog(t *testing.T) {
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	ep := &service.Episode{
		ID:        "7",
		Title:     "Jonathan Strange & Mr Norrell",
		SourceURL: "magnet:?xt=urn:btih:abc&dn=book&tr=udp://x",
	}
	entries := []episodeLogEntry{
		{At: base, Status: "added"},
		{At: base.Add(time.Minute), Status: "downloading"},
		{At: base.Add(3 * time.Minute), Status: "encoding"},
	}

	got := renderEpisodeLog(ep, entries, nil)

	// Source URL with & must be HTML-escaped so ParseModeHTML doesn't choke.
	if strings.Contains(got, "magnet:?xt=urn:btih:abc&dn") {
		t.Errorf("source URL was not HTML-escaped:\n%s", got)
	}
	if !strings.Contains(got, "&amp;dn=book") {
		t.Errorf("expected escaped ampersand in output:\n%s", got)
	}
	for _, want := range []string{
		"<b>Jonathan Strange &amp; Mr Norrell</b>",
		"00:00:00 — added",
		"00:01:00 — downloading",
		"00:03:00 — encoding",
		"/ee_7 to rename or change feed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered log missing %q:\n%s", want, got)
		}
	}

	// Footer only when a feed is passed (episode complete).
	if strings.Contains(got, "Published to") {
		t.Errorf("did not expect published footer without feed:\n%s", got)
	}
	withFooter := renderEpisodeLog(ep, entries, &service.Feed{Title: "My Feed"})
	if !strings.Contains(withFooter, "Published to <b>My Feed</b>") {
		t.Errorf("expected published footer with feed:\n%s", withFooter)
	}
}

func TestEpisodeStatusLabel(t *testing.T) {
	cases := map[service.EpisodeStatus]string{
		service.EpisodeStatusCreated:     "added",
		service.EpisodeStatusDownloading: "downloading",
		service.EpisodeStatusProcessing:  "encoding",
		service.EpisodeStatusComplete:    "done ✅",
	}
	for in, want := range cases {
		if got := episodeStatusLabel(in); got != want {
			t.Errorf("episodeStatusLabel(%v) = %q, want %q", in, got, want)
		}
	}
}
