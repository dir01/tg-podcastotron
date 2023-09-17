package service

import (
	"context"
	"database/sql"
	"github.com/hori-ryota/zaperr"
	"github.com/jmoiron/sqlx"
	"strconv"
	"strings"
	"time"
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
		return zaperr.Wrap(err, "failed to begin tx")
	}

	ctx = context.WithValue(ctx, "tx", tx)
	err = fn(ctx)
	if err != nil {
		if err := tx.Rollback(); err != nil {
			return zaperr.Wrap(err, "failed to rollback tx")
		}
		return zaperr.Wrap(err, "failed to execute tx")
	}

	if err := tx.Commit(); err != nil {
		return zaperr.Wrap(err, "failed to commit tx")
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
		return "", zaperr.Wrap(err, "failed to insert")
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
		return "", zaperr.Wrap(err, "failed to insert")
	}

	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := rows.Scan(&feedIDInt); err != nil {
			return "", zaperr.Wrap(err, "failed to scan")
		}
		break
	}

	return strconv.FormatInt(feedIDInt, 10), nil
}

// endregion

// region feeds

func (r *sqliteRepository) SaveFeed(ctx context.Context, feed *Feed) (*Feed, error) {
	db := r.dbFromContext(ctx)
	dbFeed := dbFeed{}.FromBusinessModel(feed)

	if _, err := sqlx.NamedExecContext(ctx, db, `
			INSERT INTO feeds (id, user_id, title, url) 
			VALUES (:id, :user_id, :title, :url)
			ON CONFLICT (user_id, id) DO UPDATE SET 
				user_id=:user_id,
				title=:title,
				url=:url`, dbFeed,
	); err != nil {
		return nil, zaperr.Wrap(err, "failed to insert feed")
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
		return nil, zaperr.Wrap(err, "failed to get feed")
	}

	feeds, err := r.toBusinessFeeds([]dbFeed{dbF})
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get serialized feeds")
	}
	if len(feeds) != 1 {
		return nil, zaperr.New("expected 1 feed")
	}
	return feeds[0], nil
}

func (r *sqliteRepository) GetFeedsMap(ctx context.Context, userID string, feedIDs []string) (map[string]*Feed, error) {
	db := r.dbFromContext(ctx)

	query, args, err := sqlx.Named(`
		SELECT * FROM feeds
			WHERE id IN (:ids)
			AND user_id = :user_id`,
		map[string]interface{}{
			"ids":     feedIDs,
			"user_id": userID,
		})
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to build query")
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to build IN query")
	}

	query = db.Rebind(query)

	var dbFeeds []dbFeed
	if err := sqlx.SelectContext(ctx, db, &dbFeeds, query, args...); err != nil {
		return nil, zaperr.Wrap(err, "failed to get feeds")
	}

	feeds, err := r.toBusinessFeeds(dbFeeds)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get feeds")
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
		SELECT * FROM feeds WHERE user_id = ?`, userID,
	); err != nil {
		return nil, zaperr.Wrap(err, "failed to list user feeds")
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
		return zaperr.Wrap(err, "failed to delete feeds")
	}
	return nil
}

// endregion

// region episodes

func (r *sqliteRepository) SaveEpisode(ctx context.Context, ep *Episode) (*Episode, error) {
	db := r.dbFromContext(ctx)
	dbEp := dbEpisode{}.FromBusinessModel(ep)

	if _, err := sqlx.NamedExecContext(ctx, db, `
		INSERT INTO episodes (
				id,
				user_id,
				title, 
				pub_date, 
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
				:pub_date,
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
				pub_date = :pub_date,
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
		return nil, zaperr.Wrap(err, "failed to insert ep")
	}

	return ep, nil
}

func (r *sqliteRepository) ListUserEpisodes(ctx context.Context, userID string) ([]*Episode, error) {
	var dbEpisodes []dbEpisode
	var epIDs []string
	if res, err := r.dbFromContext(ctx).QueryxContext(ctx, `
		SELECT * FROM episodes WHERE user_id = ?`, userID,
	); err != nil {
		return nil, zaperr.Wrap(err, "failed to query episodes")
	} else {
		for res.Next() {
			var dbEp dbEpisode
			if err := res.StructScan(&dbEp); err != nil {
				return nil, zaperr.Wrap(err, "failed to scan episode")
			}
			dbEpisodes = append(dbEpisodes, dbEp)
			epIDs = append(epIDs, dbEp.ID)
		}
	}

	epFeedsMap := make(map[string][]string, len(epIDs))
	if publications, err := r.ListPublicationsByEpisodeIDs(ctx, userID, epIDs); err != nil {
		return nil, zaperr.Wrap(err, "failed to list episodes feeds")
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
			return nil, zaperr.Wrap(err, "failed to convert episode to business model")
		} else {
			result = append(result, ep)
		}
	}

	return result, nil
}

func (r *sqliteRepository) ListFeedEpisodes(ctx context.Context, userID, feedID string) ([]*Episode, error) {
	publications, err := r.ListPublicationsByFeedIDs(ctx, []string{feedID}, userID)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to list publications")
	}

	episodeIDs := make([]string, 0, len(publications))
	for _, p := range publications {
		episodeIDs = append(episodeIDs, p.EpisodeID)
	}

	episodesMap, err := r.GetEpisodesMap(ctx, userID, episodeIDs)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to get episodes map")
	}

	result := make([]*Episode, 0, len(publications))
	for _, p := range publications {
		ep, ok := episodesMap[p.EpisodeID]
		if !ok {
			return nil, zaperr.New("episode not found")
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
		return nil, zaperr.Wrap(err, "failed to create query")
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to create IN query")
	}

	query = db.Rebind(query)

	var dbEpisodes []dbEpisode
	if err = sqlx.SelectContext(ctx, db, &dbEpisodes, query, args...); err != nil {
		return nil, zaperr.Wrap(err, "failed to query episodes map")
	}

	epFeedsMap := make(map[string][]string, len(episodeIDs))
	publications, err := r.ListPublicationsByEpisodeIDs(ctx, userID, episodeIDs)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to list episodes feeds")
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
			return nil, zaperr.Wrap(err, "failed to convert to business model")
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
		return zaperr.Wrap(err, "failed to create query")
	}
	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return zaperr.Wrap(err, "failed to create IN query")
	}
	query = db.Rebind(query)

	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return zaperr.Wrap(err, "failed to delete episodes")
	}

	return nil
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
			return zaperr.Wrap(err, "failed to insert feed")
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
		return nil, zaperr.Wrap(err, "failed to create query")
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to create IN query")
	}

	query = r.dbFromContext(ctx).Rebind(query)

	if err := sqlx.SelectContext(ctx, r.dbFromContext(ctx), &dbPublications, query, args...); err != nil {
		return nil, zaperr.Wrap(err, "failed to query publications by episode ids")
	}

	result := make([]*Publication, len(dbPublications))
	for i, dbP := range dbPublications {
		p, err := dbP.ToBusinessModel()
		if err != nil {
			return nil, zaperr.Wrap(err, "failed to convert to business model")
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
		return nil, zaperr.Wrap(err, "failed to create query")
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to create IN query")
	}

	query = db.Rebind(query)

	var dbPublications []dbPublication
	if err := sqlx.SelectContext(ctx, db, &dbPublications, query, args...); err != nil {
		return nil, zaperr.Wrap(err, "failed to query publications by feed ids")
	}

	result := make([]*Publication, len(dbPublications))
	for i, dbP := range dbPublications {
		p, err := dbP.ToBusinessModel()
		if err != nil {
			return nil, zaperr.Wrap(err, "failed to convert to business model")
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
		return zaperr.Wrap(err, "failed to create query")
	}

	query, args, err = sqlx.In(query, args...)
	if err != nil {
		return zaperr.Wrap(err, "failed to create IN query")
	}

	query = db.Rebind(query)

	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return zaperr.Wrap(err, "failed to delete publications")
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
			return nil, zaperr.Wrap(err, "failed to convert to business model")
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
	PubDate         string        `db:"pub_date"`
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

func (dbEpisode) FromBusinessModel(ep *Episode) *dbEpisode {
	return &dbEpisode{
		ID:              ep.ID,
		UserID:          ep.UserID,
		Title:           ep.Title,
		PubDate:         ep.PubDate.Format(time.RFC3339Nano),
		SourceURL:       ep.SourceURL,
		SourceFilepaths: strings.Join(ep.SourceFilepaths, ","),
		MediaryID:       ep.MediaryID,
		URL:             ep.URL,
		Status:          string(ep.Status),
		Duration:        ep.Duration,
		FileLenBytes:    ep.FileLenBytes,
		Format:          ep.Format,
		StorageKey:      ep.StorageKey,
	}
}

func (d dbEpisode) ToBusinessModel() (*Episode, error) {
	pubDate, err := time.Parse(time.RFC3339Nano, d.PubDate)
	if err != nil {
		return nil, zaperr.Wrap(err, "failed to parse pub date")
	}
	return &Episode{
		ID:              d.ID,
		UserID:          d.UserID,
		Title:           d.Title,
		PubDate:         pubDate,
		SourceURL:       d.SourceURL,
		SourceFilepaths: strings.Split(d.SourceFilepaths, ","),
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
	ID     string `db:"id"`
	UserID string `db:"user_id"`
	Title  string `db:"title"`
	URL    string `db:"url"`
}

func (f dbFeed) FromBusinessModel(feed *Feed) interface{} {
	return dbFeed{
		ID:     feed.ID,
		UserID: feed.UserID,
		Title:  feed.Title,
		URL:    feed.URL,
	}
}

func (f dbFeed) ToBusinessModel() (*Feed, error) {
	return &Feed{
		ID:     f.ID,
		UserID: f.UserID,
		Title:  f.Title,
		URL:    f.URL,
	}, nil
}

// endregion

// region dbPublication

type dbPublication struct {
	ID        string    `db:"id"`
	UserID    string    `db:"user_id"`
	EpisodeID string    `db:"episode_id"`
	FeedID    string    `db:"feed_id"`
	CreatedAt time.Time `db:"created_at"`
}

func (dbPublication) FromBusinessModel(p *Publication) *dbPublication {
	return &dbPublication{
		ID:        p.ID,
		UserID:    p.UserID,
		EpisodeID: p.EpisodeID,
		FeedID:    p.FeedID,
		CreatedAt: p.CreatedAt,
	}
}

func (p dbPublication) ToBusinessModel() (*Publication, error) {
	return &Publication{
		ID:        p.ID,
		UserID:    p.UserID,
		EpisodeID: p.EpisodeID,
		FeedID:    p.FeedID,
		CreatedAt: p.CreatedAt,
	}, nil
}

// endregion
