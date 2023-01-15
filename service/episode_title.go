package service

import (
	"path"
	"regexp"
	"sort"
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
		if len(suffix) < 5 { // 5 is arbitrary, but most likely 4-letter title would benefit from addition of dirname
			dirname := path.Base(path.Dir(filepaths[0]))
			if dirname != "." { // but only if dirname is not ".", that would be stupid
				return dirname + " - " + suffix
			}
		}
		return suffix
	}

	// If there are multiple files, use the directory name as the title.
	// find the longest common prefix
	prefix := filepaths[0]
	for _, filepath := range filepaths[1:] {
		for !strings.HasPrefix(filepath, prefix) {
			prefix = path.Dir(prefix)
			if prefix == "." { // reached root
				goto flatFiles
			}
		}
	}
	return path.Base(prefix)

	// If there are multiple files in the root, use the longest prefix as a title
flatFiles:
	prefix = longestCommonPrefix(filepaths)
	// account for numbering, e.g. "foo - 01", "foo - 02" -> "foo", not "foo - 0"
	prefix = regexp.MustCompile(`[\d\s_-]+$`).ReplaceAllString(prefix, "")
	return prefix
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

func longestCommonPrefix(strs []string) string {
	longestPrefix := ""

	if len(strs) < 2 {
		return longestPrefix
	}

	sort.Strings(strs)
	first := strs[0]
	last := strs[len(strs)-1]

	for i := 0; i < len(first); i++ {
		if string(last[i]) == string(first[i]) {
			longestPrefix += string(last[i])
		} else {
			break
		}
	}
	return longestPrefix
}
