package service

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
	EpisodeIDs []string
	UserID     string
}

type RegenerateFeedQueuePayload struct {
	FeedIDs []string
	UserID  string
}
