package bot

import (
	"slices"
	"strings"
	"testing"

	"tg-podcastotron/service"
)

func TestSplitFoundEpisodeIDs(t *testing.T) {
	episodesMap := map[string]*service.Episode{
		"290": {ID: "290"},
		"293": {ID: "293"},
	}
	found, missing := splitFoundEpisodeIDs([]string{"290", "291", "292", "293"}, episodesMap)
	if !slices.Equal(found, []string{"290", "293"}) {
		t.Errorf("found = %v, want [290 293]", found)
	}
	if !slices.Equal(missing, []string{"291", "292"}) {
		t.Errorf("missing = %v, want [291 292]", missing)
	}
}

func TestFormatMissingIDs(t *testing.T) {
	if got := formatMissingIDs([]string{"291", "292"}); got != "291, 292" {
		t.Errorf("formatMissingIDs = %q, want %q", got, "291, 292")
	}

	ids, err := parseIDs("1_to_1000")
	if err != nil {
		t.Fatal(err)
	}
	got := formatMissingIDs(ids)
	if !strings.HasSuffix(got, "19, 20 and 980 more") {
		t.Errorf("formatMissingIDs(1..1000) = %q, want it to end with %q", got, "19, 20 and 980 more")
	}
	if len(got) > 200 {
		t.Errorf("formatMissingIDs(1..1000) has %d characters, want 200 or less", len(got))
	}
}
