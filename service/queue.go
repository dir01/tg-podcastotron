package service

const (
	createEpisodes = "create_single_file_episode"
)

type EnqueueEpisodesPayload struct {
	URL    string
	Paths  [][]string
	UserID string
}
