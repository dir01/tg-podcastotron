package service

import (
	"testing"
)

func TestEpisodeTitle(t *testing.T) {
	tests := []struct {
		filepaths     []string
		expectedTitle string
	}{
		{
			filepaths: []string{
				"Directory/Other Directory/Some Title - 04.mp3",
			},
			expectedTitle: "Some Title - 04",
		},
		{
			filepaths: []string{
				"Directory/Other Directory/Some Title - 03.mp3",
				"Directory/Other Directory/Some Title - 04.mp3",
			},
			expectedTitle: "Other Directory",
		},
		{
			filepaths: []string{
				"Directory/Other Directory/03.mp3",
			},
			expectedTitle: "Other Directory - 03",
		},
	}
	for _, test := range tests {
		title := generateEpisodeTitle(test.filepaths)
		if title != test.expectedTitle {
			t.Errorf("expected title %q, got %q", test.expectedTitle, title)
		}
	}
}
