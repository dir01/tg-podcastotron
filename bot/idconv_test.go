package bot

import (
	"testing"
)

func TestIDConv(t *testing.T) {
	type args struct {
		ids            []string
		expectedFormat string
	}

	tests := []args{
		{ids: []string{"8"}, expectedFormat: "8"},
		{ids: []string{"1", "2"}, expectedFormat: "1_2"},
		{ids: []string{"1", "2", "3"}, expectedFormat: "1_to_3"},
		{ids: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}, expectedFormat: "1_to_10"},
		{ids: []string{"1", "2", "3", "5"}, expectedFormat: "1_to_3_5"},
		{ids: []string{"1", "3", "4", "5"}, expectedFormat: "1_3_to_5"},
		{ids: []string{"1", "3", "4"}, expectedFormat: "1_3_4"},
	}

	for _, testCase := range tests {
		idsStr, err := formatIDsCompactly(testCase.ids)
		if err != nil {
			t.Errorf("formatIDsCompactly(%v) error: %v", testCase.ids, err)
		}

		if idsStr != testCase.expectedFormat {
			t.Fatalf("formatIDsCompactly(%v) = %v, want %v", testCase.ids, idsStr, testCase.expectedFormat)
		}

		ids, err := parseIDs(idsStr)
		if err != nil {
			t.Errorf("parseIDs(%v) error: %v", idsStr, err)
		}
		if len(ids) != len(testCase.ids) {
			t.Errorf("parseIDs(%v) = %v, want %v", idsStr, ids, testCase.ids)
		}
		for i := range ids {
			if ids[i] != testCase.ids[i] {
				t.Errorf("parseIDs(%v) = %v, want %v", idsStr, ids, testCase.ids)
			}
		}
	}
}
