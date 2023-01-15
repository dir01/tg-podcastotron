package service

import (
	"testing"
)

func TestGenerateEpisodeTitle(t *testing.T) {
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
		{
			filepaths:     []string{"x-01.mp3"},
			expectedTitle: "x-01",
		},
		{
			filepaths:     []string{"Some Title - 01.mp3", "Some Title - 02.mp3"},
			expectedTitle: "Some Title",
		},
		{
			filepaths:     []string{},
			expectedTitle: "",
		},
	}
	for _, test := range tests {
		title := generateEpisodeTitle(test.filepaths)
		if title != test.expectedTitle {
			t.Errorf("expected title %q, got %q", test.expectedTitle, title)
		}
	}
}

func TestGetUpdatedEpisodeTitle(t *testing.T) {
	tests := []struct {
		oldTitle         string
		newTitlePattern  string
		expectedNewTitle string
	}{
		{
			oldTitle:         "Some Title - 04",
			newTitlePattern:  "Some Other Title",
			expectedNewTitle: "Some Other Title",
		},
		{
			oldTitle:         "Untitled - 14",
			newTitlePattern:  "My Episode - %n",
			expectedNewTitle: "My Episode - 14",
		},
		{
			oldTitle:         "Untitled - 04",
			newTitlePattern:  "My Episode - %n",
			expectedNewTitle: "My Episode - 04",
		},
		{
			oldTitle:         "Book 22 - Chapter 04",
			newTitlePattern:  "MyBook - Part %n",
			expectedNewTitle: "MyBook - Part 04",
		},
	}
	for _, test := range tests {
		newTitle := getUpdatedEpisodeTitle(test.oldTitle, test.newTitlePattern)
		if newTitle != test.expectedNewTitle {
			t.Errorf("expected title %q, got %q", test.expectedNewTitle, newTitle)
		}
	}
}
