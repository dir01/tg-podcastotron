package service

import (
	"path"
	"strings"
)

func generateEpisodeTitle(filepaths []string) string {
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
