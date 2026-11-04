package tgminiapp

import (
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strings"
	"time"

	"tg-podcastotron/service"
)

// stage is a group of episode statuses with one color and one label in the UI.
type stage struct {
	Class string // CSS class: st-<Class>
	Label string
}

var (
	stageQueued   = stage{"queued", "Queued"}
	stageDownload = stage{"download", "Downloading"}
	stageEncode   = stage{"encode", "Encoding"}
	stageUpload   = stage{"upload", "Uploading"}
	stageRetry    = stage{"retry", "Will retry"}
	stageDone     = stage{"done", "Ready"}
	stageFailed   = stage{"failed", "Failed"}
)

// barStages sets the order of the parts of a progress bar.
var barStages = []stage{stageDone, stageUpload, stageEncode, stageDownload, stageRetry, stageQueued, stageFailed}

func stageOf(s service.EpisodeStatus) stage {
	switch s {
	case service.EpisodeStatusCreated, service.EpisodeStatusPending:
		return stageQueued
	case service.EpisodeStatusDownloading:
		return stageDownload
	case service.EpisodeStatusProcessing:
		return stageEncode
	case service.EpisodeStatusUploading:
		return stageUpload
	case service.EpisodeStatusRetrying:
		return stageRetry
	case service.EpisodeStatusFailed:
		return stageFailed
	default: // complete, and old episodes with no status
		return stageDone
	}
}

// step is one step of the stepper of an episode.
type step struct {
	Label string
	State string // "done", "now", "todo", "retry" or "failed"
}

var stepLabels = []string{"Download", "Encode", "Upload", "Ready"}

// stepsOf returns the steps of an episode. A retrying or failed episode shows its
// state on the last stage that it started, from the status history.
func stepsOf(ep *service.Episode, events []*service.EpisodeEvent) []step {
	var current int
	state := "now"
	switch ep.Status {
	case service.EpisodeStatusCreated, service.EpisodeStatusPending:
		current = -1
	case service.EpisodeStatusRetrying, service.EpisodeStatusFailed:
		current, state = 0, stageOf(ep.Status).Class
		for _, e := range events {
			if i := activeStep(e.Status); i >= 0 {
				current = i
			}
		}
	default:
		if current = activeStep(ep.Status); current < 0 {
			current = len(stepLabels) // complete: all steps are done
		}
	}
	steps := make([]step, len(stepLabels))
	for i, label := range stepLabels {
		steps[i] = step{Label: label, State: "todo"}
		switch {
		case i < current:
			steps[i].State = "done"
		case i == current:
			steps[i].State = state
		}
	}
	return steps
}

// activeStep returns the index of the step that runs in status s, or -1.
func activeStep(s service.EpisodeStatus) int {
	switch s {
	case service.EpisodeStatusDownloading:
		return 0
	case service.EpisodeStatusProcessing:
		return 1
	case service.EpisodeStatusUploading:
		return 2
	}
	return -1
}

// barPart is one colored part of a progress bar.
type barPart struct {
	stage
	Count   int
	Percent float64
}

func progressBar(episodes []*service.Episode) []barPart {
	counts := map[stage]int{}
	for _, ep := range episodes {
		counts[stageOf(ep.Status)]++
	}
	var parts []barPart
	for _, st := range barStages {
		if n := counts[st]; n > 0 {
			parts = append(parts, barPart{stage: st, Count: n, Percent: 100 * float64(n) / float64(len(episodes))})
		}
	}
	return parts
}

// eventLabel is the text of one entry in the status history.
func eventLabel(e *service.EpisodeEvent) string {
	label := stageOf(e.Status).Label
	if e.Status == service.EpisodeStatusCreated {
		label = "Added"
	}
	switch {
	case e.Status == service.EpisodeStatusRetrying && e.Attempt > 0:
		return fmt.Sprintf("Attempt %s failed, will retry", attemptText(e.Attempt, e.MaxAttempts))
	case e.Status.IsFinal() || e.Status == service.EpisodeStatusCreated:
		return label
	case e.Attempt > 1:
		return fmt.Sprintf("%s (attempt %s)", label, attemptText(e.Attempt, e.MaxAttempts))
	}
	return label
}

func attemptText(attempt, max int) string {
	if max > 0 {
		return fmt.Sprintf("%d of %d", attempt, max)
	}
	return fmt.Sprint(attempt)
}

func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n < 1<<20:
		return fmt.Sprintf("%d KB", int(math.Ceil(float64(n)/(1<<10))))
	case n < 1<<30:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
}

func humanDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Minute)
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", max(m, 1))
	case m == 0:
		return fmt.Sprintf("%d h", h)
	default:
		return fmt.Sprintf("%d h %d min", h, m)
	}
}

// elapsed formats a short time span for the status history: "0:42", "12:05", "1:02:03".
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	}
	return shortDate(t, now)
}

func shortDate(t, now time.Time) string {
	if t.Year() == now.Year() {
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// daysLeft returns the number of full days until t, at least 0.
func daysLeft(t, now time.Time) int {
	return max(0, int(t.Sub(now).Hours()/24))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// totals is the number, the total length and the total size of some episodes.
type totals struct {
	Count    int
	Duration time.Duration
	Bytes    int64
}

func (t *totals) add(ep *service.Episode) {
	t.Count++
	t.Duration += ep.Duration
	t.Bytes += ep.FileLenBytes
}

func sumEpisodes(episodes []*service.Episode) totals {
	var t totals
	for _, ep := range episodes {
		t.add(ep)
	}
	return t
}

// String is for example "12 episodes · 9 h 30 min · 1.2 GB". It has no length or
// size when they are not known.
func (t totals) String() string {
	parts := []string{plural(t.Count, "episode", "episodes")}
	if d := humanDuration(t.Duration); d != "" {
		parts = append(parts, d)
	}
	if b := humanBytes(t.Bytes); b != "" {
		parts = append(parts, b)
	}
	return strings.Join(parts, " · ")
}

func joinIDs(episodes []*service.Episode) string {
	ids := make([]string, len(episodes))
	for i, ep := range episodes {
		ids[i] = ep.ID
	}
	return strings.Join(ids, ",")
}

var templateFuncs = template.FuncMap{
	"stage":      func(s service.EpisodeStatus) stage { return stageOf(s) },
	"steps":      stepsOf,
	"bar":        progressBar,
	"eventLabel": eventLabel,
	"bytes":      humanBytes,
	"duration":   humanDuration,
	"elapsed":    elapsed,
	"ago":        ago,
	"date":       shortDate,
	"daysLeft":   daysLeft,
	"plural":     plural,
	"truncate":   truncate,
	"joinIDs":    joinIDs,
	"pathEscape": url.PathEscape,
	"sub":        func(a, b time.Time) time.Duration { return a.Sub(b) },
	"add":        func(a, b int) int { return a + b },
	"contains": func(list []string, s string) bool {
		return contains(list, s)
	},
}
