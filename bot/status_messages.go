package bot

import "sync"

// statusMessages remembers the Telegram status messages that the bot edits in place.
// It is in memory only: after a restart, the next status change sends a new message
// that shows only the current status.
type statusMessages struct {
	mu       sync.Mutex
	episodes map[statusMessageKey]*episodeStatusMessage
	requests map[statusMessageKey]int // message ID of the group message
}

type statusMessageKey struct {
	userID string
	id     string // episode ID or request ID
}

type episodeStatusMessage struct {
	messageID int
	entries   []episodeLogEntry
}

// episode returns a copy of the status message of the episode, or nil.
func (s *statusMessages) episode(userID, episodeID string) *episodeStatusMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.episodes[statusMessageKey{userID, episodeID}]
	if !ok {
		return nil
	}
	return &episodeStatusMessage{messageID: msg.messageID, entries: append([]episodeLogEntry(nil), msg.entries...)}
}

func (s *statusMessages) setEpisode(userID, episodeID string, msg *episodeStatusMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.episodes == nil {
		s.episodes = make(map[statusMessageKey]*episodeStatusMessage)
	}
	s.episodes[statusMessageKey{userID, episodeID}] = msg
}

func (s *statusMessages) forgetEpisode(userID, episodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.episodes, statusMessageKey{userID, episodeID})
}

// request returns the message ID of the group message of the request, or 0.
func (s *statusMessages) request(userID, requestID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[statusMessageKey{userID, requestID}]
}

func (s *statusMessages) setRequest(userID, requestID string, messageID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests == nil {
		s.requests = make(map[statusMessageKey]int)
	}
	s.requests[statusMessageKey{userID, requestID}] = messageID
}

func (s *statusMessages) forgetRequest(userID, requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.requests, statusMessageKey{userID, requestID})
}
