package service

import (
	"bytes"
	"fmt"
	"io"
	"strconv"

	"github.com/jbub/podcasts"
)

// generateFeed makes the RSS of the feed. publicURL is the URL of the Mini App,
// which serves the covers. Without it, the items have no covers.
func generateFeed(feed *Feed, episodes []*Episode, publicURL string) (io.ReadSeeker, error) {
	p := &podcasts.Podcast{
		Title: feed.Title,
	}

	for _, e := range episodes {
		if !hasFile(e) {
			continue
		}
		var image *podcasts.ItunesImage
		if publicURL != "" && e.CoverPath() != "" {
			image = &podcasts.ItunesImage{Href: publicURL + e.CoverPath()}
		}
		p.AddItem(&podcasts.Item{
			Image:    image,
			Title:    fmt.Sprintf("%s (#%s)", e.Title, e.ID),
			GUID:     e.ID,
			PubDate:  podcasts.NewPubDate(e.CreatedAt),
			Duration: podcasts.NewDuration(e.Duration),
			Enclosure: &podcasts.Enclosure{
				URL:    e.URL,
				Length: strconv.FormatInt(e.FileLenBytes, 10),
				Type:   e.Format,
			},
		})
	}

	podcastFeed, err := p.Feed()
	if err != nil {
		return nil, fmt.Errorf("failed to generate feed: %w", err)
	}

	b := &bytes.Buffer{}
	if err = podcastFeed.Write(b); err != nil {
		return nil, fmt.Errorf("failed to write feed: %w", err)
	}

	return bytes.NewReader(b.Bytes()), nil // TODO: there must be a better way to do this
}

// hasFile tells if the episode has its audio file. An episode goes into its feeds
// when it is created, but the feed file shows it only when the file is ready.
// Old episodes can have no status, and they have files.
func hasFile(e *Episode) bool {
	switch e.Status {
	case EpisodeStatusCreated, EpisodeStatusPending, EpisodeStatusDownloading, EpisodeStatusProcessing,
		EpisodeStatusUploading, EpisodeStatusRetrying, EpisodeStatusFailed:
		return false
	}
	return true
}
