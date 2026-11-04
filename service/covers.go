package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/exp/maps"
	"tg-podcastotron/telemetry"
)

const (
	// coverFirstRead is the part of an episode file that is read first to find
	// its cover. Most ID3 tags with a cover fit in it.
	coverFirstRead = 256 << 10
	// maxCoverTagLen is the largest ID3 tag that is read to find a cover.
	maxCoverTagLen = 10 << 20
	// maxCoverBackfillErrors stops the backfill when the files cannot be read.
	maxCoverBackfillErrors = 5
)

var errNoEpisodeFile = errors.New("episode file not found")

// maxCoverLen is the largest cover that is saved. A larger picture is not a usual cover.
const maxCoverLen = 5 << 20

// Cover is a cover picture of an episode. The Mini App serves it at Episode.CoverPath.
type Cover struct {
	ID   string // random, with the file extension
	MIME string
	Data []byte
}

// GetCover returns the cover picture, or nil if there is none.
func (svc *Service) GetCover(ctx context.Context, id string) (*Cover, error) {
	return svc.repository.GetCover(ctx, id)
}

// findEpisodeCover reads the ID3 tag at the start of the episode file, and saves the
// picture of the tag. If the file has no picture, it saves an empty cover, so that
// nobody looks for it again.
func (svc *Service) findEpisodeCover(ctx context.Context, ep *Episode) error {
	fields := []any{slog.String("user_id", ep.UserID), slog.String("episode_id", ep.ID)}

	pic, err := svc.readEpisodeCover(ctx, ep.URL)
	if err != nil && !errors.Is(err, errNoEpisodeFile) {
		return telemetry.LogError(svc.logger, ctx, err, "failed to read episode cover", fields...)
	}
	if pic == nil || len(pic.Data) > maxCoverLen {
		return svc.repository.SetEpisodeCover(ctx, ep.UserID, ep.ID, "")
	}

	cover := &Cover{ID: newCoverID(pic.MIME), MIME: pic.MIME, Data: pic.Data}
	if err := svc.repository.Transaction(ctx, func(ctx context.Context) error {
		if err := svc.repository.SaveCover(ctx, cover); err != nil {
			return err
		}
		return svc.repository.SetEpisodeCover(ctx, ep.UserID, ep.ID, cover.ID)
	}); err != nil {
		return telemetry.LogError(svc.logger, ctx, err, "failed to save episode cover", fields...)
	}
	ep.CoverID = cover.ID
	return nil
}

// newCoverID returns a random ID with the extension of the picture type. The ID
// is in a public URL, so it must not be possible to guess it.
func newCoverID(mime string) string {
	ext := ".jpg"
	if mime == "image/png" {
		ext = ".png"
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "") + ext
}

// readEpisodeCover returns the cover in the ID3 tag at the start of the file at url,
// or nil if there is none. It reads only the tag.
func (svc *Service) readEpisodeCover(ctx context.Context, url string) (*id3Picture, error) {
	if url == "" {
		return nil, errNoEpisodeFile
	}
	head, err := svc.readFileStart(ctx, url, coverFirstRead)
	if err != nil {
		return nil, err
	}
	tagLen, ok := id3TagLen(head)
	if !ok || tagLen > maxCoverTagLen {
		return nil, nil
	}
	if tagLen > len(head) {
		if head, err = svc.readFileStart(ctx, url, tagLen); err != nil {
			return nil, err
		}
	}
	pic, ok := id3FrontCover(head)
	if !ok {
		return nil, nil
	}
	return pic, nil
}

// readFileStart reads the first n bytes of the file at url with a Range request.
func (svc *Service) readFileStart(ctx context.Context, url string, n int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", n-1))
	resp, err := svc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to read episode file: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		return nil, errNoEpisodeFile
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent:
		return nil, fmt.Errorf("failed to read episode file: status %d", resp.StatusCode)
	}
	// A server can ignore Range and send all of the file: read only n bytes.
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(n)))
	if err != nil {
		return nil, fmt.Errorf("failed to read episode file: %w", err)
	}
	return b, nil
}

// findCoversOfCompleteEpisodes finds the covers of the episodes that are complete
// now. An error is only logged: the episode is complete without a cover too.
func (svc *Service) findCoversOfCompleteEpisodes(ctx context.Context, episodes []*Episode) {
	for _, ep := range episodes {
		if ep.Status == EpisodeStatusComplete && ep.CoverID == "" {
			_ = svc.findEpisodeCover(ctx, ep) // logged
		}
	}
}

// RunCoverBackfill finds the covers of the complete episodes that nobody looked for
// a cover for yet, and makes their feeds again. It stops after a few errors in a row:
// then the next start of the app tries again.
func (svc *Service) RunCoverBackfill(ctx context.Context) {
	episodes, err := svc.repository.ListEpisodesWithoutCover(ctx)
	if err != nil {
		svc.logger.ErrorContext(ctx, "failed to list episodes without cover", slog.Any("error", err))
		return
	}
	if len(episodes) == 0 {
		return
	}
	svc.logger.InfoContext(ctx, "looking for episode covers", slog.Int("episodes", len(episodes)))

	withCover := map[string][]string{} // episode IDs by user ID
	found, errorsInARow := 0, 0
	for _, ep := range episodes {
		if ctx.Err() != nil {
			return
		}
		if err := svc.findEpisodeCover(ctx, ep); err != nil {
			if errorsInARow++; errorsInARow >= maxCoverBackfillErrors {
				svc.logger.WarnContext(ctx, "stopped looking for episode covers after errors", slog.Int("found", found))
				break
			}
			continue
		}
		errorsInARow = 0
		if ep.CoverID != "" {
			found++
			withCover[ep.UserID] = append(withCover[ep.UserID], ep.ID)
		}
	}
	svc.logger.InfoContext(ctx, "found episode covers", slog.Int("found", found), slog.Int("episodes", len(episodes)))

	// The feeds of these episodes get the covers.
	for userID, epIDs := range withCover {
		publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
		if err != nil {
			svc.logger.ErrorContext(ctx, "failed to list publications", slog.Any("error", err))
			continue
		}
		feeds := map[string]bool{}
		for _, p := range publications {
			feeds[p.FeedID] = true
		}
		_ = svc.enqueueFeedsRegeneration(ctx, userID, maps.Keys(feeds)) // logged
	}
}
