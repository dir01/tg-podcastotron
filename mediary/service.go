package mediary

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

func New(mediaryURL string) *Service {
	return &Service{
		mediaryURL: mediaryURL,
		retryDelays: []time.Duration{
			1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
			16 * time.Second, 32 * time.Second, 64 * time.Second, 128 * time.Second,
		},
	}
}

type Service struct {
	mediaryURL  string
	retryDelays []time.Duration
}

type Metadata struct {
	URL   string         `json:"url"`
	Name  string         `json:"name"`
	Files []FileMetadata `json:"files"`
}

type FileMetadata struct {
	Path     string `json:"path"`
	LenBytes int64  `json:"length_bytes"`
}

func (svc *Service) FetchMetadata(ctx context.Context, mediaURL string) (*Metadata, error) {
	var lastErr error
	for idx, delay := range svc.retryDelays {
		if idx > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		fullURL := fmt.Sprintf("%s/metadata/long-polling?url=%s", svc.mediaryURL, mediaURL)
		log.Printf("Fetching metadata from %s", fullURL)
		resp, err := http.Get(fullURL)
		if err != nil {
			lastErr = fmt.Errorf("failed to call mediary API: %w", err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("mediary returned status code %d", resp.StatusCode)
			continue
		}

		var metadata Metadata
		if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
			return nil, fmt.Errorf("error decoding mediary response: %w", err)
		}

		return &metadata, nil
	}

	return nil, lastErr
}

func (svc *Service) IsValidURL(ctx context.Context, url string) (bool, error) {
	return true, nil
}
