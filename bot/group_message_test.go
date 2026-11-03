package bot

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tg-podcastotron/service"
)

func TestRenderGroupMessage(t *testing.T) {
	start := time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC)
	newEps := func(statuses ...service.EpisodeStatus) []*service.Episode {
		eps := make([]*service.Episode, len(statuses))
		for i, s := range statuses {
			eps[i] = &service.Episode{
				ID:        fmt.Sprint(295 + i),
				SourceURL: "magnet:?xt=urn:btih:abc&dn=Pimsleur",
				CreatedAt: start,
				Status:    s,
			}
		}
		return eps
	}
	feed := &service.Feed{Title: "Default"}

	t.Run("in progress", func(t *testing.T) {
		eps := newEps(service.EpisodeStatusPending, service.EpisodeStatusCreated,
			service.EpisodeStatusProcessing, service.EpisodeStatusComplete)
		got := renderGroupMessage(eps, start.Add(time.Hour), nil)
		for _, want := range []string{"<b>Pimsleur</b>", "<b>4 episodes</b>", "2 queued\n", "1 encoding\n", "1 done ✅\n",
			"Started: 2026-11-03 18:00 UTC", "/ee_295_to_298"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
		for _, notWant := range []string{"downloading", "Finished", "Published"} {
			if strings.Contains(got, notWant) {
				t.Errorf("unexpected %q in:\n%s", notWant, got)
			}
		}
	})

	t.Run("finished", func(t *testing.T) {
		eps := newEps(service.EpisodeStatusComplete, service.EpisodeStatusFailed)
		got := renderGroupMessage(eps, start.Add(90*time.Minute), feed)
		for _, want := range []string{"1 done ✅\n", "1 failed ❌\n",
			"Finished: 2026-11-03 19:30 UTC (took 01:30:00)", "Published to <b>Default</b>"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
	})

	t.Run("length does not depend on the number of episodes", func(t *testing.T) {
		statuses := make([]service.EpisodeStatus, 1000)
		for i := range statuses {
			statuses[i] = service.EpisodeStatusDownloading
		}
		if got := renderGroupMessage(newEps(statuses...), start, nil); !fitsTelegram(got) {
			t.Errorf("message is too long: %d bytes", len(got))
		}
	})
}
