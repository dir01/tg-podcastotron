package service

import (
	"reflect"
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
			filepaths: []string{
				"01_Some title.mp3",
				"02_Some title.mp3",
				"03_Some title.mp3",
			},
			expectedTitle: "Some title",
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
		title := titleFromFilepaths(test.filepaths)
		if title != test.expectedTitle {
			t.Errorf("expected title %q, got %q", test.expectedTitle, title)
		}
	}
}

func TestGetUpdatedEpisodeTitle(t *testing.T) {
	tests := []struct {
		episodes         []*Episode
		newTitlePattern  string
		expectedTitleMap map[string]string
	}{
		{
			episodes:         []*Episode{{ID: "1", Title: "Some Title - 04"}},
			newTitlePattern:  "Some Other Title",
			expectedTitleMap: map[string]string{"1": "Some Other Title"},
		},
		{
			episodes:        []*Episode{{ID: "1", Title: "Untitled - 01_02"}, {ID: "2", Title: "Untitled - 02_05"}},
			newTitlePattern: "My Episode - %v",
			expectedTitleMap: map[string]string{
				"1": "My Episode - 1_02",
				"2": "My Episode - 2_05",
			},
		},
		{
			episodes: []*Episode{
				{ID: "3", Title: "FOO"},
				{ID: "512", Title: "FOO"},
			},
			newTitlePattern: "Bar - %id",
			expectedTitleMap: map[string]string{
				"3":   "Bar - 003",
				"512": "Bar - 512",
			},
		},
	}
	for _, test := range tests {
		titleMap := getUpdatedEpisodeTitle(test.episodes, test.newTitlePattern)
		if !reflect.DeepEqual(test.expectedTitleMap, titleMap) {
			t.Errorf("expected title map %v, got %v", test.expectedTitleMap, titleMap)
		}
	}
}
