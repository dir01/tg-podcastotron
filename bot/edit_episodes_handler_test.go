package bot

import (
	"slices"
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
