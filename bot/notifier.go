package bot

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"tg-podcastotron/service"
	"tg-podcastotron/telemetry"
)

// handleEpisodesStatusChanges runs in the trace of the job that made the changes.
// Without this, the logs of this handler have no trace_id.
func (ub *UndercastBot) handleEpisodesStatusChanges(ctx context.Context, batch service.EpisodeStatusChanges) {
	ctx = trace.ContextWithSpanContext(ctx, batch.SpanContext)
	ctx, span := telemetry.StartSpan(ctx, "bot.episodes_status_changes",
		attribute.Int("episodes.count", len(batch.Changes)))
	defer span.End()

	ub.notifyFinished(ctx, batch.Changes)
}

// notifyFinished sends one message for each request whose last episode finished
// with these changes. The progress itself is in the Mini App: the chat gets no
// message for each status change.
func (ub *UndercastBot) notifyFinished(ctx context.Context, changes []service.EpisodeStatusChange) {
	type key struct{ userID, requestID string }
	var requests []key
	seen := map[key]bool{}
	for _, c := range changes {
		if !c.NewStatus.IsFinal() {
			continue
		}
		k := key{c.Episode.UserID, c.Episode.RequestID()}
		if k.requestID == "" {
			// An episode from before requests existed.
			ub.notify(ctx, k.userID, &service.Request{SourceURL: c.Episode.SourceURL, Episodes: []*service.Episode{c.Episode}}, "/episodes/"+c.Episode.ID)
			continue
		}
		if !seen[k] {
			seen[k] = true
			requests = append(requests, k)
		}
	}

	for _, k := range requests {
		req, err := ub.service.GetRequest(ctx, k.userID, k.requestID)
		if err != nil {
			ub.logger.ErrorContext(ctx, "failed to get request", slog.String("request_id", k.requestID), slog.Any("error", err))
			continue
		}
		if req == nil || !req.Finished() || !ub.notified.add(k.userID+"/"+k.requestID) {
			continue
		}
		ub.notify(ctx, k.userID, req, "/requests/"+req.ID)
	}
}

func (ub *UndercastBot) notify(ctx context.Context, userID string, req *service.Request, path string) {
	feeds, err := ub.service.ListFeeds(ctx, userID)
	if err != nil {
		ub.logger.ErrorContext(ctx, "failed to list feeds", slog.Any("error", err))
	}
	chatID, err := ub.repository.GetChatID(ctx, userID)
	if err != nil || chatID <= 0 {
		// In a private chat, the chat ID is the user ID.
		if _, scanErr := fmt.Sscan(userID, &chatID); scanErr != nil {
			ub.logger.ErrorContext(ctx, "failed to get chat id", slog.String("user_id", userID), slog.Any("error", err))
			return
		}
	}
	markup := ub.openButton("Open", path)
	markup.InlineKeyboard = append(copyFeedLinkButtons(readyFeeds(req, feeds)), markup.InlineKeyboard...)
	ub.send(ctx, chatID, finishedMessage(req, feeds), markup)
}

// maxCopyButtons is the maximum number of "copy the feed link" buttons in a message.
const maxCopyButtons = 3

// copyTextMaxLen is the maximum length of the text of a copy_text button (Telegram Bot API).
const copyTextMaxLen = 256

// copyFeedLinkButtons returns one row with a "copy the link" button for each feed.
// A tap on the button copies the feed link, so the user can add it to a podcast app.
func copyFeedLinkButtons(feeds []*service.Feed) [][]models.InlineKeyboardButton {
	var rows [][]models.InlineKeyboardButton
	for _, f := range feeds {
		if f.URL == "" || len(f.URL) > copyTextMaxLen || len(rows) == maxCopyButtons {
			continue
		}
		text := "Copy the feed link"
		if len(feeds) > 1 {
			text = "Copy the link: " + truncate(f.Title, 40)
		}
		rows = append(rows, []models.InlineKeyboardButton{{Text: text, CopyText: &models.CopyTextButton{Text: f.URL}}})
	}
	return rows
}

// finishedMessage is the text of the message about a finished request.
func finishedMessage(req *service.Request, feeds []*service.Feed) string {
	total := len(req.Episodes)
	ready := req.Count(service.EpisodeStatusComplete)
	failed := total - ready

	title := req.Title()
	if total == 1 && req.Episodes[0].Title != "" {
		title = req.Episodes[0].Title
	}

	var b strings.Builder
	switch {
	case ready == 0:
		b.WriteString("❌ ")
	case failed > 0:
		b.WriteString("⚠️ ")
	default:
		b.WriteString("✅ ")
	}
	if title != "" {
		fmt.Fprintf(&b, "<b>%s</b>\n", html.EscapeString(truncate(title, 200)))
	}

	var inFeeds string
	if names := feedNames(req, feeds); names != "" {
		inFeeds = " in " + names
	}
	switch {
	case total == 1 && ready == 1:
		fmt.Fprintf(&b, "The episode is ready%s.", inFeeds)
	case total == 1:
		b.WriteString("The episode failed.")
	case ready == 0:
		fmt.Fprintf(&b, "All %d episodes failed.", total)
	case failed > 0:
		fmt.Fprintf(&b, "%d of %d episodes are ready%s. %d failed.", ready, total, inFeeds, failed)
	default:
		fmt.Fprintf(&b, "%d episodes are ready%s.", total, inFeeds)
	}
	return b.String()
}

// readyFeeds returns the feeds of the complete episodes.
func readyFeeds(req *service.Request, feeds []*service.Feed) []*service.Feed {
	var result []*service.Feed
	for _, f := range feeds {
		for _, ep := range req.Episodes {
			if ep.Status == service.EpisodeStatusComplete && contains(ep.FeedIDs, f.ID) {
				result = append(result, f)
				break
			}
		}
	}
	return result
}

// feedNames returns the names of the feeds of the complete episodes, for example "<b>A</b> and <b>B</b>".
func feedNames(req *service.Request, feeds []*service.Feed) string {
	var names []string
	for _, f := range readyFeeds(req, feeds) {
		names = append(names, "<b>"+html.EscapeString(truncate(f.Title, 100))+"</b>")
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// notifiedRequests remembers the requests that got a message. Two status batches
// can both see a finished request, and the user must get only one message.
// It is in memory only: after a restart, a message can come again.
type notifiedRequests struct {
	mu   sync.Mutex
	keys map[string]time.Time
}

const notifiedRequestsTTL = 24 * time.Hour

// add returns true if the key was not there.
func (n *notifiedRequests) add(key string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	if n.keys == nil {
		n.keys = map[string]time.Time{}
	}
	for k, t := range n.keys {
		if now.Sub(t) > notifiedRequestsTTL {
			delete(n.keys, k)
		}
	}
	if _, ok := n.keys[key]; ok {
		return false
	}
	n.keys[key] = now
	return true
}
