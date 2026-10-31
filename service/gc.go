package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"tg-podcastotron/telemetry"
)

// GCGracePeriod protects recent objects from collection, so that an upload racing
// with the creation of its episode row is never mistaken for garbage.
const GCGracePeriod = 24 * time.Hour

// gcPrefix is the only prefix gc lists or deletes under. The app's IAM policy is
// scoped to the episodes/ and feeds/ prefixes, and feeds are never garbage-collected.
const gcPrefix = "episodes/"

// GCReport describes episode files in S3 that no episode refers to (garbage),
// and episodes whose file is missing from S3.
type GCReport struct {
	ScannedObjects int
	ScannedBytes   int64

	Garbage      []S3Object
	GarbageBytes int64
	ByOwner      []GCOwnerStats // garbage grouped by user key prefix, largest first

	// Missing lists complete episodes whose file does not exist. Reported only, never fixed by gc.
	Missing []MissingEpisodeFile
}

type GCOwnerStats struct {
	KeyPrefix string
	UserID    string // empty if no episode of a known user has this key prefix
	Count     int
	Bytes     int64
	Oldest    time.Time
	Newest    time.Time
}

type MissingEpisodeFile struct {
	Episode    *Episode
	StorageKey string
	FeedIDs    []string
}

// ScanStorageGarbage finds episode files that are not referenced by any episode and
// are older than GCGracePeriod. It does not delete anything.
func (svc *Service) ScanStorageGarbage(ctx context.Context) (*GCReport, error) {
	objects, err := svc.s3Store.List(ctx, gcPrefix)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "gc: failed to list episode files")
	}

	episodes, err := svc.repository.ListAllEpisodes(ctx)
	if err != nil {
		return nil, telemetry.LogError(svc.logger, ctx, err, "gc: failed to list episodes")
	}
	if len(episodes) == 0 && len(objects) > 0 {
		// Most likely the wrong database: refuse rather than call every file garbage.
		return nil, telemetry.LogError(svc.logger, ctx, fmt.Errorf("no episodes in database, but %d episode files in storage", len(objects)), "gc: refusing to scan")
	}

	// Legacy content-keyed episodes can share one file, so a key can have many episodes.
	referenced := make(map[string][]*Episode, len(episodes))
	userByPrefix := make(map[string]string)
	for _, ep := range episodes {
		if key := svc.extractEpisodeS3Key(ep); key != "" {
			referenced[key] = append(referenced[key], ep)
		}
		userByPrefix[svc.getUserKeyPrefix(ep.UserID)] = ep.UserID
	}

	report := &GCReport{}
	existing := make(map[string]bool, len(objects))
	owners := make(map[string]*GCOwnerStats)
	cutoff := time.Now().Add(-GCGracePeriod)

	for _, obj := range objects {
		existing[obj.Key] = true
		report.ScannedObjects++
		report.ScannedBytes += obj.Size

		keyPrefix, ok := collectableKeyPrefix(obj.Key)
		if !ok || len(referenced[obj.Key]) > 0 || obj.LastModified.After(cutoff) {
			continue
		}

		report.Garbage = append(report.Garbage, obj)
		report.GarbageBytes += obj.Size

		owner, ok := owners[keyPrefix]
		if !ok {
			owner = &GCOwnerStats{KeyPrefix: keyPrefix, UserID: userByPrefix[keyPrefix], Oldest: obj.LastModified, Newest: obj.LastModified}
			owners[keyPrefix] = owner
		}
		owner.Count++
		owner.Bytes += obj.Size
		if obj.LastModified.Before(owner.Oldest) {
			owner.Oldest = obj.LastModified
		}
		if obj.LastModified.After(owner.Newest) {
			owner.Newest = obj.LastModified
		}
	}

	for _, owner := range owners {
		report.ByOwner = append(report.ByOwner, *owner)
	}
	sort.Slice(report.ByOwner, func(i, j int) bool { return report.ByOwner[i].Bytes > report.ByOwner[j].Bytes })
	sort.Slice(report.Garbage, func(i, j int) bool { return report.Garbage[i].Key < report.Garbage[j].Key })

	missingByUser := make(map[string][]*MissingEpisodeFile)
	for key, eps := range referenced {
		if existing[key] {
			continue
		}
		for _, ep := range eps {
			if ep.Status == EpisodeStatusComplete {
				missingByUser[ep.UserID] = append(missingByUser[ep.UserID], &MissingEpisodeFile{Episode: ep, StorageKey: key})
			}
		}
	}
	for userID, missing := range missingByUser {
		epIDs := make([]string, len(missing))
		byID := make(map[string]*MissingEpisodeFile, len(missing))
		for i, m := range missing {
			epIDs[i] = m.Episode.ID
			byID[m.Episode.ID] = m
		}
		publications, err := svc.repository.ListPublicationsByEpisodeIDs(ctx, userID, epIDs)
		if err != nil {
			return nil, telemetry.LogError(svc.logger, ctx, err, "gc: failed to list publications", slog.String("user_id", userID))
		}
		for _, p := range publications {
			byID[p.EpisodeID].FeedIDs = append(byID[p.EpisodeID].FeedIDs, p.FeedID)
		}
		for _, m := range missing {
			report.Missing = append(report.Missing, *m)
		}
	}
	sort.Slice(report.Missing, func(i, j int) bool {
		a, b := report.Missing[i].Episode, report.Missing[j].Episode
		if a.UserID != b.UserID {
			return a.UserID < b.UserID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})

	return report, nil
}

// DeleteStorageGarbage deletes those of keys that are still garbage according to a
// fresh scan, so that a file which gained an owner since the scan the admin
// confirmed is never deleted.
func (svc *Service) DeleteStorageGarbage(ctx context.Context, keys []string) (deleted int, deletedBytes int64, err error) {
	report, err := svc.ScanStorageGarbage(ctx)
	if err != nil {
		return 0, 0, err
	}

	stillGarbage := make(map[string]int64, len(report.Garbage))
	for _, obj := range report.Garbage {
		stillGarbage[obj.Key] = obj.Size
	}

	toDelete := make([]string, 0, len(keys))
	for _, key := range keys {
		if size, ok := stillGarbage[key]; ok {
			toDelete = append(toDelete, key)
			deletedBytes += size
		}
	}

	svc.logger.InfoContext(ctx, "gc: deleting garbage episode files",
		slog.Int("requested", len(keys)), slog.Int("deleting", len(toDelete)), slog.Int64("bytes", deletedBytes))

	if err := svc.s3Store.DeleteMany(ctx, toDelete); err != nil {
		return 0, 0, telemetry.LogError(svc.logger, ctx, err, "gc: failed to delete garbage episode files")
	}

	return len(toDelete), deletedBytes, nil
}

// collectableKeyPrefix returns the user key prefix of an episode file key shaped
// like episodes/<user key prefix>/<file>. Keys of any other shape are never collected.
func collectableKeyPrefix(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0]+"/" != gcPrefix || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	return parts[1], true
}
