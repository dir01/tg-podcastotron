package service

import (
	"time"
)

const (
	queueEventCreateEpisodes     = "create_episodes"
	queueEventStartEpisodeUpload = "start_episode_upload"
	queueEventPollEpisodesStatus = "poll_episodes_status"
	queueEventRegenerateFeed     = "regenerate_feed"
)

type ProcessingType string

const (
	ProcessingTypeConcatenate    ProcessingType = "concatenate"
	ProcessingTypeUploadOriginal ProcessingType = "upload_original"
)

type CreateEpisodesQueuePayload struct {
	// RequestID identifies this create request. Together with a variant's index it forms
	// the episode's creation key, so retries find the episodes they already created.
	// Payloads enqueued before it existed have it empty; a hash of the payload is used instead.
	RequestID string
	URL       string
	// VariantsPerEpisode is a slice of slices of variants. Each slice represents an episode. Each episode can have multiple variants.
	VariantsPerEpisode [][]string
	UserID             string
	ProcessingType     ProcessingType
}

type PollEpisodesStatusQueuePayload struct {
	EpisodeIDs       []string
	UserID           string
	PollingStartedAt *time.Time
	Delay            *time.Duration // current delay; used to compute next delay for exponential backoff
	RequeueCount     int
	// Attempts is the last mediary attempt number of each episode, by episode ID.
	// A larger attempt number with the same status means that mediary retried the job
	// between two polls.
	Attempts map[string]int `json:",omitempty"`
}

type RegenerateFeedQueuePayload struct {
	FeedIDs []string
	UserID  string
}

// StartEpisodeUploadQueuePayload asks to start the mediary job that produces one
// already-persisted episode's file. Handling it is idempotent: once the episode has
// a mediary job, it is never started again.
type StartEpisodeUploadQueuePayload struct {
	UserID         string
	EpisodeID      string
	URL            string
	Variants       []string
	ProcessingType ProcessingType
}
