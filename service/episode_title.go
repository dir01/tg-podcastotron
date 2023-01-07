package service

import (
	"path"
	"regexp"
	"strings"
)

func generateEpisodeTitle(filepaths []string) string {
	if len(filepaths) == 0 {
		return ""
	}
	// If there is only one file, use the filename as the title.
	if len(filepaths) == 1 {
		base := path.Base(filepaths[0])
		suffix := strings.TrimSuffix(base, path.Ext(base))
		if len(suffix) > 5 {
			return suffix
		}

		return path.Base(path.Dir(filepaths[0])) + " - " + suffix
	}

	// If there are multiple files, use the directory name as the title.
	// find the longest common prefix
	prefix := filepaths[0]
	for _, filepath := range filepaths[1:] {
		for !strings.HasPrefix(filepath, prefix) {
			prefix = path.Dir(prefix)
		}
	}

	return path.Base(prefix)
}

func getUpdatedEpisodeTitle(oldTitle string, newTitlePattern string) (newTitle string) {
	if !strings.Contains(newTitlePattern, "%n") {
		return newTitlePattern
	}
	re := regexp.MustCompile(`(\d+)[^\d]*$`)
	matches := re.FindStringSubmatch(oldTitle)
	if len(matches) == 0 {
		return newTitlePattern
	}
	return strings.Replace(newTitlePattern, "%n", matches[1], 1)
}
