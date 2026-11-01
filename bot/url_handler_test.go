package bot

import (
	"html"
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

func TestNewEpisodeLogEntry(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		change     service.EpisodeStatusChange
		wantStatus string
		wantDetail string
	}{
		{
			name:       "first attempt has no attempt number",
			change:     service.EpisodeStatusChange{NewStatus: service.EpisodeStatusDownloading, Attempt: 1, MaxAttempts: 6},
			wantStatus: "downloading",
		},
		{
			name:       "old mediary without attempts",
			change:     service.EpisodeStatusChange{NewStatus: service.EpisodeStatusProcessing},
			wantStatus: "encoding",
		},
		{
			name:       "retrying",
			change:     service.EpisodeStatusChange{NewStatus: service.EpisodeStatusRetrying, Attempt: 2, MaxAttempts: 6, Error: "stalled"},
			wantStatus: "attempt 2 of 6 failed, will retry",
			wantDetail: "stalled",
		},
		{
			name:       "retrying without attempt number",
			change:     service.EpisodeStatusChange{NewStatus: service.EpisodeStatusRetrying},
			wantStatus: "failed, will retry",
		},
		{
			name:       "retry attempt",
			change:     service.EpisodeStatusChange{NewStatus: service.EpisodeStatusDownloading, Attempt: 3, MaxAttempts: 6, Error: "stalled"},
			wantStatus: "downloading (attempt 3 of 6)",
			wantDetail: "stalled",
		},
		{
			name:       "retry attempt with unknown maximum",
			change:     service.EpisodeStatusChange{NewStatus: service.EpisodeStatusUploading, Attempt: 2},
			wantStatus: "uploading (attempt 2)",
		},
		{
			name:       "failed",
			change:     service.EpisodeStatusChange{NewStatus: service.EpisodeStatusFailed, Attempt: 6, MaxAttempts: 6, Error: "timed out"},
			wantStatus: "failed ❌",
			wantDetail: "timed out",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newEpisodeLogEntry(at, tc.change)
			if got.At != at || got.Status != tc.wantStatus || got.Detail != tc.wantDetail {
				t.Errorf("got %+v, want status %q and detail %q", got, tc.wantStatus, tc.wantDetail)
			}
		})
	}
}

func TestRenderEpisodeLog_Detail(t *testing.T) {
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	entries := []episodeLogEntry{
		{At: base, Status: "downloading"},
		{At: base.Add(time.Hour), Status: "failed ❌", Detail: "3 of 35 files <complete> & stalled"},
	}

	got := renderEpisodeLog(&service.Episode{ID: "7"}, entries, nil)

	want := "01:00:00 — failed ❌\n    ↳ <i>3 of 35 files &lt;complete&gt; &amp; stalled</i>\n"
	if !strings.Contains(got, want) {
		t.Errorf("rendered log missing %q:\n%s", want, got)
	}
}

func TestRenderEpisodeLog_LongDetailIsTruncated(t *testing.T) {
	entries := []episodeLogEntry{{At: time.Now(), Status: "failed ❌", Detail: strings.Repeat("é", 500)}}

	got := renderEpisodeLog(&service.Episode{ID: "7"}, entries, nil)

	want := "<i>" + strings.Repeat("é", maxEpisodeLogDetailLen-1) + "…</i>"
	if !strings.Contains(got, want) {
		t.Errorf("expected the detail to be truncated to %d runes:\n%s", maxEpisodeLogDetailLen, got)
	}
}

func TestRenderEpisodeLog_FitsTelegramLimit(t *testing.T) {
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	ep := &service.Episode{
		ID:        "7",
		Title:     "Book",
		SourceURL: "magnet:?xt=urn:btih:abc&dn=" + strings.Repeat("tracker&", 400), // about 3200 characters
	}
	short := []episodeLogEntry{{At: base, Status: "added"}, {At: base.Add(time.Minute), Status: "downloading"}}

	if fitsTelegram("<b>a</b>" + strings.Repeat("&amp;", maxEpisodeLogLen)) {
		t.Errorf("expected tags to count 0 and each entity to count 1")
	}
	if !fitsTelegram("<b>a</b>" + strings.Repeat("&amp;", maxEpisodeLogLen-1)) {
		t.Errorf("expected tags to count 0 and each entity to count 1")
	}

	// A message that fits is not changed.
	if got := renderEpisodeLog(ep, short, nil); !strings.Contains(got, html.EscapeString(ep.SourceURL)) {
		t.Errorf("expected the full source URL when the message fits:\n%s", got)
	}

	entries := []episodeLogEntry{{At: base, Status: "added"}}
	for i := 1; i <= 6; i++ {
		entries = append(entries,
			episodeLogEntry{At: base.Add(time.Duration(i) * time.Hour), Status: "downloading"},
			episodeLogEntry{At: base.Add(time.Duration(i)*time.Hour + time.Minute), Status: "attempt failed", Detail: strings.Repeat("e", 300)},
		)
	}
	entries = append(entries, episodeLogEntry{At: base.Add(7 * time.Hour), Status: "failed ❌", Detail: "the last error"})

	got := renderEpisodeLog(ep, entries, nil)
	if !fitsTelegram(got) {
		t.Fatalf("message is too long:\n%s", got)
	}
	for _, want := range []string{"00:00:00 — added", "07:00:00 — failed ❌", "↳ <i>the last error</i>", "/ee_7"} {
		if !strings.Contains(got, want) {
			t.Errorf("shortened message missing %q:\n%s", want, got)
		}
	}

	// Many entries without details: the oldest entries are removed, the first and the last stay.
	many := []episodeLogEntry{{At: base, Status: "added"}}
	for i := 1; i <= 300; i++ {
		many = append(many, episodeLogEntry{At: base.Add(time.Duration(i) * time.Minute), Status: "downloading"})
	}
	got = renderEpisodeLog(ep, many, nil)
	if !fitsTelegram(got) {
		t.Fatalf("message is too long:\n%s", got)
	}
	for _, want := range []string{"00:00:00 — added\n…\n", "05:00:00 — downloading"} {
		if !strings.Contains(got, want) {
			t.Errorf("shortened message missing %q:\n%s", want, got)
		}
	}
}
