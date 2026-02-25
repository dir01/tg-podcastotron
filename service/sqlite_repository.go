package service

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

func NewSqliteRepository(db *sql.DB) Repository {
	return &sqliteRepository{db: sqlx.NewDb(db, "sqlite3")}
}

type sqliteRepository struct {
	db *sqlx.DB
}

// region transaction

func (r *sqliteRepository) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}

	ctx = context.WithValue(ctx, "tx", tx) //nolint:staticcheck
	err = fn(ctx)
	if err != nil {
		if err := tx.Rollback(); err != nil {
			return fmt.Errorf("failed to rollback tx: %w", err)
		}
		return fmt.Errorf("failed to execute tx: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit tx: %w", err)
	}

	return nil
}

func (r *sqliteRepository) dbFromContext(ctx context.Context) sqlx.ExtContext {
	if tx, ok := ctx.Value("tx").(*sqlx.Tx); ok {
		return tx
	} else {
		return r.db
	}
}

// endregion

// region local ids

func (r *sqliteRepository) NextEpisodeID(ctx context.Context, userID string) (epID string, err error) {
	db := r.dbFromContext(ctx)

	var episodeID int64
	err = db.QueryRowxContext(ctx, `
		INSERT INTO local_ids (user_id, episode_id, feed_id) VALUES (?, 1, 0)
		ON CONFLICT (user_id) DO UPDATE SET episode_id=episode_id+1
		RETURNING episode_id
	`, userID).Scan(&episodeID)
	if err != nil {
		return "", fmt.Errorf("failed to insert: %w", err)
	}

	return strconv.FormatInt(episodeID, 10), nil
}

func (r *sqliteRepository) NextFeedID(ctx context.Context, userID string) (feedID string, err error) {
	db := r.dbFromContext(ctx)

	var feedIDInt int64
	rows, err := db.QueryxContext(ctx, `
		INSERT INTO local_ids (user_id, feed_id, episode_id) VALUES (?, 1, 0)
		ON CONFLICT (user_id) DO UPDATE SET feed_id=feed_id+1
		RETURNING feed_id
	`, userID, &feedIDInt)
	if err != nil {
		return "", fmt.Errorf("failed to insert: %w", err)
	}

	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := rows.Scan(&feedIDInt); err != nil {
			return "", fmt.Errorf("failed to scan: %w", err)
		}
		break //nolint:staticcheck //loop is unconditionally terminated intentionally
	}

	return strconv.FormatInt(feedIDInt, 10), nil
}

// endregion

// region feeds

func (r *sqliteRepository) SaveFeed(ctx context.Context, feed *Feed) (*Feed, error) {
	db := r.dbFromContext(ctx)
	dbFeed := dbFeed{}.FromBusinessModel(feed)

	if _, err := sqlx.NamedExecContext(ctx, db, `
			INSERT INTO feeds (id, user_id, title, url, is_permanent) 
			VALUES (:id, :user_id, :title, :url, :is_permanent)
			ON CONFLICT (user_id, id) DO UPDATE SET 
				user_id=:user_id,
				title=:title,
				url=:url,
				is_permanent=:is_permanent
	`, dbFeed); err != nil {
		return nil, fmt.Errorf("failed to insert feed: %w", err)
	}

	return feed, nil
}

func (r *sqliteRepository) GetFeed(ctx context.Context, userID, feedID string) (*Feed, error) {
	db := r.dbFromContext(ctx)

	var dbF dbFeed
	if err := sqlx.GetContext(ctx, db, &dbF, `
		SELECT * FROM feeds WHERE id = ? AND user_id = ?`, feedID, userID,
	); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to get feed: %w", err)
	}

	feeds, err := r.toBusinessFeeds([]dbFeed{dbF})
	if err != nil {
		return nil, fmt.Errorf("failed to get serialized feeds: %w", err)
	}
	if len(feeds) != 1 {
		return nil, fmt.Errorf("expected 1 feed")
	}
	return feeds[0], nil
}

func (r *sqliteRepository) GetFeedsMap(ctx context.Context, userID string, feedIDs []string) (map[string]*Feed, error) {
	db := r.dbFromContext(ctx)

	if len(feedIDs) == 0 {
		return map[string]*Feed{}, nil
	}

	query, args, err := sqlx.Named(`
		SELECT * FROM feeds
			WHERE id IN (:ids)
			AND user_id = :user_id`,
		map[string]interface{}{
			"ids":     feedIDs,
			"user_id": userID,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %w", err)
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to build IN query: %w", err)
	}

	query = db.Rebind(query)

	var dbFeeds []dbFeed
	if err := sqlx.SelectContext(ctx, db, &dbFeeds, query, args...); err != nil {
		return nil, fmt.Errorf("failed to get feeds: %w", err)
	}

	feeds, err := r.toBusinessFeeds(dbFeeds)
	if err != nil {
		return nil, fmt.Errorf("failed to get feeds: %w", err)
	}

	result := make(map[string]*Feed, len(feeds))
	for _, f := range feeds {
		result[f.ID] = f
	}

	return result, nil
}

func (r *sqliteRepository) ListUserFeeds(ctx context.Context, userID string) ([]*Feed, error) {
	var dbFeeds []dbFeed
	if err := sqlx.SelectContext(ctx, r.dbFromContext(ctx), &dbFeeds, `
		SELECT * FROM feeds WHERE user_id = ? ORDER BY id`, userID,
	); err != nil {
		return nil, fmt.Errorf("failed to list user feeds: %w", err)
	}
	return r.toBusinessFeeds(dbFeeds)
}

func (r *sqliteRepository) DeleteFeed(ctx context.Context, userID string, feedID string) error {
	_, err := r.dbFromContext(ctx).ExecContext(ctx, `
		DELETE FROM feeds 
			WHERE id = ?
		  	AND user_id = ?`, feedID, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete feeds: %w", err)
	}
	return nil
}

// endregion

// region episodes

func (r *sqliteRepository) SaveEpisode(ctx context.Context, ep *Episode) (*Episode, error) {
	db := r.dbFromContext(ctx)
	dbEp, err := dbEpisode{}.FromBusinessModel(ep)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize episode: %w", err)
	}

	if _, err := sqlx.NamedExecContext(ctx, db, `
		INSERT INTO episodes (
				id,
				user_id,
				title, 
			  	created_at,
				updated_at, 
				source_url, 
				source_filepaths, 
				mediary_id, 
				url, 
				status, 
				duration, 
				file_len_bytes, 
				format, 
				storage_key
		) VALUES (
				:id,
				:user_id,
				:title,
		        :created_at,
				:updated_at,
				:source_url,
				:source_filepaths,
				:mediary_id,
				:url,
				:status,
				:duration,
				:file_len_bytes,
				:format,
				:storage_key
	  	) ON CONFLICT (user_id, id) DO UPDATE SET
				title = :title,
				updated_at = :updated_at,
				source_url = :source_url,
				source_filepaths = :source_filepaths,
				mediary_id = :mediary_id,
				url = :url,
				status = :status,
				duration = :duration,
				file_len_bytes = :file_len_bytes,
				format = :format,
				storage_key = :storage_key`, dbEp,
	); err != nil {
		return nil, fmt.Errorf("failed to insert ep: %w", err)
	}

	ep, err = dbEp.ToBusinessModel()
	if err != nil {
		return nil, fmt.Errorf("failed to convert to business model: %w", err)
	}

	return ep, nil
}

func (r *sqliteRepository) ListUserEpisodes(ctx context.Context, userID string) ([]*Episode, error) {
	var dbEpisodes []dbEpisode
	var epIDs []string
	if res, err := r.dbFromContext(ctx).QueryxContext(ctx, `
		SELECT * FROM episodes WHERE user_id = ?`, userID,
	); err != nil {
		return nil, fmt.Errorf("failed to query episodes: %w", err)
	} else {
		for res.Next() {
			var dbEp dbEpisode
			if err := res.StructScan(&dbEp); err != nil {
				return nil, fmt.Errorf("failed to scan episode: %w", err)
			}
			dbEpisodes = append(dbEpisodes, dbEp)
			epIDs = append(epIDs, dbEp.ID)
		}
	}

	epFeedsMap := make(map[string][]string, len(epIDs))
	if publications, err := r.ListPublicationsByEpisodeIDs(ctx, userID, epIDs); err != nil {
		return nil, fmt.Errorf("failed to list episodes feeds: %w", err)
	} else {
		for _, p := range publications {
			if _, ok := epFeedsMap[p.EpisodeID]; !ok {
				epFeedsMap[p.EpisodeID] = []string{p.FeedID}
			} else {
				epFeedsMap[p.EpisodeID] = append(epFeedsMap[p.EpisodeID], p.FeedID)
			}
		}
	}

	result := make([]*Episode, 0, len(dbEpisodes))
	for _, dbEp := range dbEpisodes {
		if ep, err := dbEp.ToBusinessModel(); err != nil {
			return nil, fmt.Errorf("failed to convert episode to business model: %w", err)
		} else {
			result = append(result, ep)
		}
	}

	return result, nil
}

func (r *sqliteRepository) ListFeedEpisodes(ctx context.Context, userID, feedID string) ([]*Episode, error) {
	publications, err := r.ListPublicationsByFeedIDs(ctx, []string{feedID}, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list publications: %w", err)
	}

	episodeIDs := make([]string, 0, len(publications))
	for _, p := range publications {
		episodeIDs = append(episodeIDs, p.EpisodeID)
	}

	episodesMap, err := r.GetEpisodesMap(ctx, userID, episodeIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to get episodes map: %w", err)
	}

	result := make([]*Episode, 0, len(publications))
	for _, p := range publications {
		ep, ok := episodesMap[p.EpisodeID]
		if !ok {
			return nil, fmt.Errorf("episode not found")
		}
		result = append(result, ep)
	}

	return result, nil
}

func (r *sqliteRepository) GetEpisodesMap(ctx context.Context, userID string, episodeIDs []string) (map[string]*Episode, error) {
	if len(episodeIDs) == 0 {
		return map[string]*Episode{}, nil
	}

	db := r.dbFromContext(ctx)

	query, args, err := sqlx.Named(`
		SELECT * FROM episodes 
			WHERE user_id=:user_id
			AND id IN (:ids)`,
		map[string]interface{}{
			"user_id": userID,
			"ids":     episodeIDs,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create query: %w", err)
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create IN query: %w", err)
	}

	query = db.Rebind(query)

	var dbEpisodes []dbEpisode
	if err = sqlx.SelectContext(ctx, db, &dbEpisodes, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query episodes map: %w", err)
	}

	epFeedsMap := make(map[string][]string, len(episodeIDs))
	publications, err := r.ListPublicationsByEpisodeIDs(ctx, userID, episodeIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list episodes feeds: %w", err)
	}
	for _, ef := range publications {
		if _, ok := epFeedsMap[ef.EpisodeID]; !ok {
			epFeedsMap[ef.EpisodeID] = []string{ef.FeedID}
		} else {
			epFeedsMap[ef.EpisodeID] = append(epFeedsMap[ef.EpisodeID], ef.FeedID)
		}
	}

	result := make(map[string]*Episode)
	for _, dbEp := range dbEpisodes {
		ep, err := dbEp.ToBusinessModel()
		if err != nil {
			return nil, fmt.Errorf("failed to convert to business model: %w", err)
		}
		result[ep.ID] = ep
	}

	return result, nil
}

func (r *sqliteRepository) DeleteEpisodes(ctx context.Context, userID string, episodeIDs []string) error {
	db := r.dbFromContext(ctx)
	query, args, err := sqlx.Named(`
		DELETE FROM episodes 
			WHERE id IN (:ids) 
			AND user_id = :user_id`,
		map[string]interface{}{
			"ids":     episodeIDs,
			"user_id": userID,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create query: %w", err)
	}
	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return fmt.Errorf("failed to create IN query: %w", err)
	}
	query = db.Rebind(query)

	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to delete episodes: %w", err)
	}

	return nil
}

func (r *sqliteRepository) ListExpiredEpisodes(ctx context.Context, maxAge time.Duration) ([]*Episode, error) {
	db := r.dbFromContext(ctx)

	minUpdatedAt := timeToStr(time.Now().UTC().Add(-maxAge))
	query, args, err := sqlx.Named(`
		SELECT e.* FROM episodes e
		WHERE e.updated_at < :min_updated_at
		AND NOT EXISTS (
			SELECT 1
			FROM publications p
			JOIN feeds f ON p.feed_id = f.id AND p.user_id = f.user_id
			WHERE f.is_permanent = true
			AND p.episode_id = e.id
			AND p.user_id = e.user_id
		)
	`, map[string]interface{}{
		"min_updated_at": minUpdatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create query: %w", err)
	}

	var dbEpisodes []dbEpisode
	if err := sqlx.SelectContext(ctx, db, &dbEpisodes, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query episodes: %w", err)
	}

	result := make([]*Episode, len(dbEpisodes))
	for idx, dbEp := range dbEpisodes {
		if ep, err := dbEp.ToBusinessModel(); err != nil {
			return nil, fmt.Errorf("failed to convert to business model: %w", err)
		} else {
			result[idx] = ep
		}
	}

	return result, nil
}

// endregion

// region publications

func (r *sqliteRepository) BulkInsertPublications(ctx context.Context, publications []*Publication) error {
	db := r.dbFromContext(ctx)
	// TODO: implement real bulk insert sometime
	for _, p := range publications {
		dbP := dbPublication{}.FromBusinessModel(p)
		if _, err := sqlx.NamedExecContext(ctx, db, `
			INSERT INTO publications (user_id, feed_id, episode_id, created_at)
			VALUES (:user_id, :feed_id, :episode_id, :created_at)`,
			dbP,
		); err != nil {
			return fmt.Errorf("failed to insert feed: %w", err)
		}
	}
	return nil
}

func (r *sqliteRepository) ListPublicationsByEpisodeIDs(ctx context.Context, userID string, episodeIDs []string) ([]*Publication, error) {
	if len(episodeIDs) == 0 {
		return []*Publication{}, nil
	}

	var dbPublications []dbPublication

	query, args, err := sqlx.Named(`
		SELECT * FROM publications 
			WHERE user_id=:user_id 
			AND episode_id IN (:episode_ids)`,
		map[string]interface{}{
			"user_id":     userID,
			"episode_ids": episodeIDs,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create query: %w", err)
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create IN query: %w", err)
	}

	query = r.dbFromContext(ctx).Rebind(query)

	if err := sqlx.SelectContext(ctx, r.dbFromContext(ctx), &dbPublications, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query publications by episode ids: %w", err)
	}

	result := make([]*Publication, len(dbPublications))
	for i, dbP := range dbPublications {
		p, err := dbP.ToBusinessModel()
		if err != nil {
			return nil, fmt.Errorf("failed to convert to business model: %w", err)
		}
		result[i] = p
	}

	return result, nil
}

func (r *sqliteRepository) ListPublicationsByFeedIDs(ctx context.Context, feedIDs []string, userID string) ([]*Publication, error) {
	if len(feedIDs) == 0 {
		return []*Publication{}, nil
	}

	db := r.dbFromContext(ctx)

	query, args, err := sqlx.Named(`
		SELECT * FROM publications 
			WHERE user_id=:user_id 
			AND feed_id IN (:feed_ids)`,
		map[string]interface{}{
			"user_id":  userID,
			"feed_ids": feedIDs,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create query: %w", err)
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create IN query: %w", err)
	}

	query = db.Rebind(query)

	var dbPublications []dbPublication
	if err := sqlx.SelectContext(ctx, db, &dbPublications, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query publications by feed ids: %w", err)
	}

	result := make([]*Publication, len(dbPublications))
	for i, dbP := range dbPublications {
		p, err := dbP.ToBusinessModel()
		if err != nil {
			return nil, fmt.Errorf("failed to convert to business model: %w", err)
		}
		result[i] = p
	}

	return result, nil
}

func (r *sqliteRepository) DeletePublications(ctx context.Context, userID string, publicationIDs []string) error {
	if len(publicationIDs) == 0 {
		return nil
	}

	db := r.dbFromContext(ctx)

	query, args, err := sqlx.Named(`
		DELETE FROM publications
			WHERE user_id=:user_id
			AND id IN (:ids)`,
		map[string]interface{}{
			"user_id": userID,
			"ids":     publicationIDs,
		})
	if err != nil {
		return fmt.Errorf("failed to create query: %w", err)
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return fmt.Errorf("failed to create IN query: %w", err)
	}

	query = db.Rebind(query)

	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to delete publications: %w", err)
	}

	return nil
}

// endregion

// region private

func (r *sqliteRepository) toBusinessFeeds(dbFeeds []dbFeed) ([]*Feed, error) {
	result := make([]*Feed, len(dbFeeds))
	for i, dbF := range dbFeeds {
		f, err := dbF.ToBusinessModel()
		if err != nil {
			return nil, fmt.Errorf("failed to convert to business model: %w", err)
		}
		result[i] = f
	}

	return result, nil
}

// endregion

// region dbEpisode

type dbEpisode struct {
	ID              string        `db:"id"`
	UserID          string        `db:"user_id"`
	Title           string        `db:"title"`
	CreatedAt       string        `db:"created_at"`
	UpdatedAt       string        `db:"updated_at"`
	SourceURL       string        `db:"source_url"`
	SourceFilepaths string        `db:"source_filepaths"`
	MediaryID       string        `db:"mediary_id"`
	URL             string        `db:"url"`
	Status          string        `db:"status"`
	Duration        time.Duration `db:"duration"`
	FileLenBytes    int64         `db:"file_len_bytes"`
	Format          string        `db:"format"`
	StorageKey      string        `db:"storage_key"`
}

func (dbEpisode) FromBusinessModel(ep *Episode) (*dbEpisode, error) {
	if ep == nil {
		return nil, fmt.Errorf("ep is nil")
	}
	if ep.CreatedAt.IsZero() {
		return nil, fmt.Errorf(".CreatedAt is zero")
	}
	if ep.UpdatedAt.IsZero() {
		return nil, fmt.Errorf(".UpdatedAt is zero")
	}
	return &dbEpisode{
		ID:              ep.ID,
		UserID:          ep.UserID,
		Title:           ep.Title,
		CreatedAt:       timeToStr(ep.CreatedAt),
		UpdatedAt:       timeToStr(ep.UpdatedAt),
		SourceURL:       ep.SourceURL,
		SourceFilepaths: strings.Join(ep.SourceFilepaths, ","),
		MediaryID:       ep.MediaryID,
		URL:             ep.URL,
		Status:          string(ep.Status),
		Duration:        ep.Duration,
		FileLenBytes:    ep.FileLenBytes,
		Format:          ep.Format,
		StorageKey:      ep.StorageKey,
	}, nil
}

func (d dbEpisode) ToBusinessModel() (*Episode, error) {
	createdAt, err := strToTime(d.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to parse created_at: %w", err)
	}

	updatedAt, err := strToTime(d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to parse updated_at: %w", err)
	}

	var sourceFilePaths []string
	if d.SourceFilepaths != "" {
		sourceFilePaths = strings.Split(d.SourceFilepaths, ",")
	}

	return &Episode{
		ID:              d.ID,
		UserID:          d.UserID,
		Title:           d.Title,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		SourceURL:       d.SourceURL,
		SourceFilepaths: sourceFilePaths,
		MediaryID:       d.MediaryID,
		URL:             d.URL,
		Status:          EpisodeStatus(d.Status),
		Duration:        d.Duration,
		FileLenBytes:    d.FileLenBytes,
		Format:          d.Format,
		StorageKey:      d.StorageKey,
	}, nil
}

// endregion

// region dbFeed

type dbFeed struct {
	ID          string `db:"id"`
	UserID      string `db:"user_id"`
	Title       string `db:"title"`
	URL         string `db:"url"`
	IsPermanent bool   `db:"is_permanent"`
}

func (f dbFeed) FromBusinessModel(feed *Feed) interface{} {
	return dbFeed{
		ID:          feed.ID,
		UserID:      feed.UserID,
		Title:       feed.Title,
		URL:         feed.URL,
		IsPermanent: feed.IsPermanent,
	}
}

func (f dbFeed) ToBusinessModel() (*Feed, error) {
	return &Feed{
		ID:          f.ID,
		UserID:      f.UserID,
		Title:       f.Title,
		URL:         f.URL,
		IsPermanent: f.IsPermanent,
	}, nil
}

// endregion

// region dbPublication

type dbPublication struct {
	ID        string `db:"id"`
	UserID    string `db:"user_id"`
	EpisodeID string `db:"episode_id"`
	FeedID    string `db:"feed_id"`
	CreatedAt string `db:"created_at"`
}

func (dbPublication) FromBusinessModel(p *Publication) *dbPublication {
	return &dbPublication{
		ID:        p.ID,
		UserID:    p.UserID,
		EpisodeID: p.EpisodeID,
		FeedID:    p.FeedID,
		CreatedAt: timeToStr(p.CreatedAt),
	}
}

func (p dbPublication) ToBusinessModel() (*Publication, error) {
	createdAt, err := strToTime(p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to parse created at: %w", err)
	}
	return &Publication{
		ID:        p.ID,
		UserID:    p.UserID,
		EpisodeID: p.EpisodeID,
		FeedID:    p.FeedID,
		CreatedAt: createdAt,
	}, nil
}

// endregion

// region dates

// SQLite's recommended datetime format is the textual format "YYYY-MM-DD HH:MM:SS"
// But somehow it doesn't work well with sqlx: what I get back looks like 2023-09-20T09:52:07Z
const sqliteTimeFormat = time.RFC3339

func timeToStr(t time.Time) string {
	return t.UTC().Format(sqliteTimeFormat)
}

func strToTime(s string) (time.Time, error) {
	t, err := time.Parse(sqliteTimeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse time: %w", err)
	}
	return t.UTC(), nil
}

// endregion
