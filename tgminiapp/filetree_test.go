package tgminiapp

import (
	"reflect"
	"testing"

	"tg-podcastotron/service"
)

func variants(paths ...string) []service.Variant {
	vs := make([]service.Variant, len(paths))
	for i, p := range paths {
		size := int64(10)
		vs[i] = service.Variant{ID: p, LenBytes: &size}
	}
	return vs
}

func names(nodes []*fileNode) []string {
	var result []string
	for _, n := range nodes {
		result = append(result, n.Name)
	}
	return result
}

func TestBuildFileTree(t *testing.T) {
	root := buildFileTree(variants(
		"Book/CD 10/track.mp3",
		"Book/CD 2/track.mp3",
		"Book/CD 2/notes.txt",
		"Book/cover.jpg",
		"Book/Extras/Deep/notes.txt",
	))

	// The single top folder "Book" is skipped.
	if got, want := names(root.Children), []string{"CD 2", "CD 10", "Extras/Deep", "cover.jpg"}; !equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	if root.Files != 5 || root.Size != 50 {
		t.Errorf("root files, size = %d, %d; want 5, 50", root.Files, root.Size)
	}
	if root.TopExt != "mp3" || root.TopExtCount != 2 || !root.ShowExt() || !root.ShowActions() {
		t.Errorf("root top extension = %q (%d), want mp3 (2) with all buttons", root.TopExt, root.TopExtCount)
	}

	cd2 := root.Children[0]
	if cd2.TopExt != "mp3" || cd2.TopExtCount != 1 || !cd2.ShowExt() {
		t.Errorf("CD 2 top extension = %q (%d), want mp3 (1)", cd2.TopExt, cd2.TopExtCount)
	}
	if file := cd2.Children[0]; !file.IsFile || file.Path != "Book/CD 2/notes.txt" {
		t.Errorf("first file of CD 2 = %+v", file)
	}
	// A folder with one file needs no buttons.
	if cd10 := root.Children[1]; cd10.ShowActions() {
		t.Errorf("CD 10 has buttons")
	}
}

func TestBuildFileTree__OneExtensionHasNoExtButton(t *testing.T) {
	root := buildFileTree(variants("b.mp3", "a.mp3"))
	if got, want := names(root.Children), []string{"a.mp3", "b.mp3"}; !equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	if root.ShowExt() || !root.ShowActions() {
		t.Errorf("ShowExt, ShowActions = %v, %v; want false, true", root.ShowExt(), root.ShowActions())
	}
}

func TestNaturalLess(t *testing.T) {
	ordered := []string{"1 a", "2", "02b", "10", "Chapter 9", "chapter 10", "x"}
	for i := 0; i+1 < len(ordered); i++ {
		if !naturalLess(ordered[i], ordered[i+1]) {
			t.Errorf("naturalLess(%q, %q) = false", ordered[i], ordered[i+1])
		}
		if naturalLess(ordered[i+1], ordered[i]) {
			t.Errorf("naturalLess(%q, %q) = true", ordered[i+1], ordered[i])
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEpisodeVariantsKeepTheSelectionOrder(t *testing.T) {
	files := []string{"2_bar/a.mp3", "2_bar/b.mp3", "1_foo/a.mp3"}

	variants, processing := episodeVariants(files, false)
	if want := [][]string{{"2_bar/a.mp3"}, {"2_bar/b.mp3"}, {"1_foo/a.mp3"}}; !reflect.DeepEqual(variants, want) || processing != service.ProcessingTypeUploadOriginal {
		t.Errorf("separate: %v, %s", variants, processing)
	}
	variants, processing = episodeVariants(files, true)
	if want := [][]string{files}; !reflect.DeepEqual(variants, want) || processing != service.ProcessingTypeConcatenate {
		t.Errorf("join: %v, %s", variants, processing)
	}
	// One file is never joined.
	if _, processing = episodeVariants(files[:1], true); processing != service.ProcessingTypeUploadOriginal {
		t.Errorf("join of one file: %s", processing)
	}
}
