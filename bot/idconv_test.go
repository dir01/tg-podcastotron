package bot

import (
	"testing"
)

func TestIDConv(t *testing.T) {
	type args struct {
		ids            []string
		expectedFormat string
		expectedParsed []string
	}

	tests := []args{
		{ids: []string{"8"}, expectedFormat: "8", expectedParsed: nil},
		{ids: []string{"1", "2"}, expectedFormat: "1_2", expectedParsed: nil},
		{ids: []string{"1", "2", "3"}, expectedFormat: "1_to_3", expectedParsed: nil},
		{ids: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}, expectedFormat: "1_to_10", expectedParsed: nil},
		{ids: []string{"1", "2", "3", "5"}, expectedFormat: "1_to_3_5", expectedParsed: nil},
		{ids: []string{"1", "3", "4", "5"}, expectedFormat: "1_3_to_5", expectedParsed: nil},
		{ids: []string{"1", "3", "4"}, expectedFormat: "1_3_4", expectedParsed: nil},
		{ids: []string{"10", "9", "8", "10", "12", "8"}, expectedFormat: "8_to_10_12", expectedParsed: []string{"8", "9", "10", "12"}},
	}

	for _, testCase := range tests {
		idsStr, err := formatIDsCompactly(testCase.ids)
		if err != nil {
			t.Errorf("formatIDsCompactly(%v) error: %v", testCase.ids, err)
		}

		if idsStr != testCase.expectedFormat {
			t.Fatalf("formatIDsCompactly(%v) = %v, want %v", testCase.ids, idsStr, testCase.expectedFormat)
		}

		if testCase.expectedParsed == nil {
			testCase.expectedParsed = testCase.ids
		}
		ids, err := parseIDs(idsStr)
		if err != nil {
			t.Errorf("parseIDs(%v) error: %v", idsStr, err)
		}
		if len(ids) != len(testCase.expectedParsed) {
			t.Errorf("parseIDs(%v) = %v, want %v", idsStr, ids, testCase.ids)
		}
		for i := range ids {
			if ids[i] != testCase.expectedParsed[i] {
				t.Errorf("parseIDs(%v) = %v, want %v", idsStr, ids, testCase.expectedParsed)
			}
		}
	}
}
