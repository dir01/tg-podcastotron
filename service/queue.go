package service

import (
	"time"
)

const (
	queueEventCreateEpisodes     = "create_episodes"
	queueEventPollEpisodesStatus = "poll_episodes_status"
	queueEventRegenerateFeed     = "regenerate_feed"
)

type ProcessingType string

const (
	ProcessingTypeConcatenate    ProcessingType = "concatenate"
	ProcessingTypeUploadOriginal ProcessingType = "upload_original"
)

type CreateEpisodesQueuePayload struct {
	URL string
	// VariantsPerEpisode is a slice of slices of variants. Each slice represents an episode. Each episode can have multiple variants.
	VariantsPerEpisode [][]string
	UserID             string
	ProcessingType     ProcessingType
}

type PollEpisodesStatusQueuePayload struct {
	EpisodeIDs       []string
	UserID           string
	PollingStartedAt *time.Time
	Delay            *time.Duration
	PollAfter        *time.Time
	RequeueCount     int
}

type RegenerateFeedQueuePayload struct {
	FeedIDs []string
	UserID  string
}
