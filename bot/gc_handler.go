package bot

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"tg-podcastotron/service"
)

const (
	gcCallbackPrefix  = "gc:"
	gcConfirmTTL      = time.Hour
	gcMaxListedOwners = 20
	gcMaxListedMissed = 20
	gcMaxListedFeeds  = 5
	// gcReportMaxLen keeps the report below the 4096-character limit of a Telegram
	// message. If the report is too long, the bot cannot send it with its buttons.
	gcReportMaxLen = 3500
	// gcListLineReserve is the space kept for the "…and N more" line of a list.
	gcListLineReserve = 50
)

// gcPendingScans remembers scans awaiting the admin's confirmation, so that the
// confirm button deletes exactly the files the admin was shown (minus any that
// stopped being garbage in the meantime).
type gcPendingScans struct {
	mu    sync.Mutex
	scans map[string]gcPendingScan
}

type gcPendingScan struct {
	keys      []string
	expiresAt time.Time
}

func (p *gcPendingScans) put(keys []string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scans == nil {
		p.scans = make(map[string]gcPendingScan)
	}
	for token, scan := range p.scans {
		if time.Now().After(scan.expiresAt) {
			delete(p.scans, token)
		}
	}
	token := uuid.NewString()
	p.scans[token] = gcPendingScan{keys: keys, expiresAt: time.Now().Add(gcConfirmTTL)}
	return token
}

// take removes and returns the scan for token, if it exists and has not expired.
func (p *gcPendingScans) take(token string) ([]string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	scan, ok := p.scans[token]
	delete(p.scans, token)
	if !ok || time.Now().After(scan.expiresAt) {
		return nil, false
	}
	return scan.keys, true
}

// gcHandler (/gc, admin only) reports episode files that no episode refers to and
// offers to delete them.
func (ub *UndercastBot) gcHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	chatID := ub.extractChatID(update)

	if isAdmin, err := ub.auth.IsAdmin(ctx, ub.extractUsername(update)); err != nil {
		ub.handleError(ctx, chatID, err)
		return
	} else if !isAdmin {
		ub.sendTextMessage(ctx, chatID, "unknown command")
		return
	}

	ub.sendTextMessage(ctx, chatID, "Scanning storage…")

	report, err := ub.service.ScanStorageGarbage(ctx)
	if err != nil {
		ub.handleError(ctx, chatID, fmt.Errorf("failed to scan storage: %w", err))
		return
	}

	params := &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      renderGCReport(report),
		ParseMode: models.ParseModeHTML,
	}
	if len(report.Garbage) > 0 {
		keys := make([]string, len(report.Garbage))
		for i, obj := range report.Garbage {
			keys[i] = obj.Key
		}
		token := ub.gcScans.put(keys)
		params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: fmt.Sprintf("🗑 Delete %d files (%s)", len(keys), formatGiB(report.GarbageBytes)), CallbackData: gcCallbackPrefix + "ok:" + token},
			{Text: "Cancel", CallbackData: gcCallbackPrefix + "no:" + token},
		}}}
	}

	if _, err := ub.bot.SendMessage(ctx, params); err != nil {
		ub.logger.ErrorContext(ctx, "failed to send gc report", slog.Any("error", err))
	}
}

func (ub *UndercastBot) gcCallbackHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	cq := update.CallbackQuery
	chatID := ub.extractChatID(update)

	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID}); err != nil {
		ub.logger.ErrorContext(ctx, "failed to answer gc callback", slog.Any("error", err))
	}

	if isAdmin, err := ub.auth.IsAdmin(ctx, ub.extractUsername(update)); err != nil || !isAdmin {
		return
	}

	action, token, _ := strings.Cut(strings.TrimPrefix(cq.Data, gcCallbackPrefix), ":")
	keys, ok := ub.gcScans.take(token)

	var result string
	switch {
	case !ok:
		result = "This scan has expired or was already handled. Run /gc again."
	case action != "ok":
		result = "Cancelled, nothing was deleted."
	default:
		deleted, bytes, err := ub.service.DeleteStorageGarbage(ctx, keys)
		if err != nil {
			ub.handleError(ctx, chatID, fmt.Errorf("failed to delete garbage: %w", err))
			return
		}
		result = fmt.Sprintf("Deleted %d of %d files (%s).", deleted, len(keys), formatGiB(bytes))
		if deleted < len(keys) {
			result += " The rest were no longer garbage on re-scan."
		}
	}

	// Drop the buttons so the scan cannot be confirmed twice.
	if msg := cq.Message.Message; msg != nil {
		if _, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{ChatID: chatID, MessageID: msg.ID}); err != nil {
			ub.logger.WarnContext(ctx, "failed to remove gc buttons", slog.Any("error", err))
		}
	}
	ub.sendTextMessage(ctx, chatID, result)
}

func renderGCReport(r *service.GCReport) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "<b>Storage GC</b>\nScanned %d episode files, %s.\n\n", r.ScannedObjects, formatGiB(r.ScannedBytes))

	if len(r.Garbage) == 0 {
		fmt.Fprintf(&sb, "No garbage: every file older than %s belongs to an episode.\n", service.GCGracePeriod)
	} else {
		fmt.Fprintf(&sb, "<b>Garbage:</b> %d files, %s, not referenced by any episode and older than %s.\n",
			len(r.Garbage), formatGiB(r.GarbageBytes), service.GCGracePeriod)
		for i, o := range r.ByOwner {
			owner := "unknown user"
			if o.UserID != "" {
				owner = "user " + o.UserID
			}
			line := fmt.Sprintf("• %s <code>%s…</code>: %d files, %s, %s → %s\n",
				html.EscapeString(owner), html.EscapeString(shorten(o.KeyPrefix, 8)), o.Count, formatGiB(o.Bytes),
				o.Oldest.UTC().Format("2006-01-02"), o.Newest.UTC().Format("2006-01-02"))
			// The owners list can use at most half of the report, so the missing files fit too.
			if i == gcMaxListedOwners || sb.Len()+len(line) > gcReportMaxLen/2-gcListLineReserve {
				fmt.Fprintf(&sb, "…and %d more users\n", len(r.ByOwner)-i)
				break
			}
			sb.WriteString(line)
		}
	}

	if len(r.Missing) > 0 {
		fmt.Fprintf(&sb, "\n<b>Missing files</b> (reported only, not fixed): %d complete episodes have no file.\n", len(r.Missing))
		for i, m := range r.Missing {
			feeds := "no feed"
			if len(m.FeedIDs) > gcMaxListedFeeds {
				feeds = fmt.Sprintf("feeds %s and %d more", strings.Join(m.FeedIDs[:gcMaxListedFeeds], ", "), len(m.FeedIDs)-gcMaxListedFeeds)
			} else if len(m.FeedIDs) > 0 {
				feeds = "feeds " + strings.Join(m.FeedIDs, ", ")
			}
			line := fmt.Sprintf("• user %s, episode #%s (%s): %s\n",
				html.EscapeString(m.Episode.UserID), html.EscapeString(m.Episode.ID), html.EscapeString(feeds),
				html.EscapeString(shorten(m.Episode.Title, 60)))
			if i == gcMaxListedMissed || sb.Len()+len(line) > gcReportMaxLen-gcListLineReserve {
				fmt.Fprintf(&sb, "…and %d more\n", len(r.Missing)-i)
				break
			}
			sb.WriteString(line)
		}
	}

	return sb.String()
}

func formatGiB(bytes int64) string {
	return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
}

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
