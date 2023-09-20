package service

import (
	"bytes"
	"fmt"
	"io"
	"strconv"

	"github.com/jbub/podcasts"
)

func generateFeed(feed *Feed, episodes []*Episode) (io.ReadSeeker, error) {
	p := &podcasts.Podcast{
		Title: feed.Title,
	}

	for _, e := range episodes {
		p.AddItem(&podcasts.Item{
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
