package service

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestGenerateFeedHasOnlyEpisodesWithFiles(t *testing.T) {
	now := time.Now()
	episodes := []*Episode{
		{ID: "1", Title: "ready", Status: EpisodeStatusComplete, URL: "https://example.com/1.mp3", CreatedAt: now, CoverID: "c1.jpg"},
		{ID: "2", Title: "old", Status: "", URL: "https://example.com/2.mp3", CreatedAt: now},
		{ID: "3", Title: "downloading", Status: EpisodeStatusDownloading, URL: "https://example.com/3.mp3", CreatedAt: now},
		{ID: "4", Title: "failed", Status: EpisodeStatusFailed, URL: "https://example.com/4.mp3", CreatedAt: now},
	}
	r, err := generateFeed(&Feed{Title: "Feed"}, episodes, "https://app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	xml := string(b)
	for _, want := range []string{"ready (#1)", "old (#2)", `<itunes:image href="https://app.example.com/covers/c1.jpg">`} {
		if !strings.Contains(xml, want) {
			t.Errorf("feed has no %q", want)
		}
	}
	for _, unwanted := range []string{"downloading", "failed"} {
		if strings.Contains(xml, unwanted) {
			t.Errorf("feed has %q", unwanted)
		}
	}
}
