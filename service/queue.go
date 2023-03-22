package service

import (
	"time"
)

const (
	createEpisodes     = "create_episodes"
	pollEpisodesStatus = "poll_episodes_status"
	regenerateFeed     = "regenerate_feed"
)

type CreateEpisodesQueuePayload struct {
	URL    string
	Paths  [][]string
	UserID string
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
