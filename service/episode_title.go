package service

import (
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

func titleFromFilepaths(filepaths []string) string {
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

	// If there are multiple files in the root, use the longest prefix or longest suffix as a title
flatFiles:
	prefix, suffix := longestCommonPrefixAndSuffix(filepaths)
	if prefix > suffix {
		// account for numbering, e.g. "foo - 01", "foo - 02" -> "foo", not "foo - 0"
		prefix = regexp.MustCompile(`[\d\s_-]+$`).ReplaceAllString(prefix, "")
		return prefix
	} else {
		suffix = strings.Replace(suffix, path.Ext(suffix), "", 1)
		suffix = strings.Trim(suffix, "_ -")
		return suffix
	}
}

func titleFromSourceURL(sourceURL string) string {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("dn") // magnet link title
}

func getUpdatedEpisodeTitle(episodes []*Episode, newTitlePattern string) map[string]string {
	result := make(map[string]string, len(episodes))

	hasVariablePart := strings.Contains(newTitlePattern, "%v")
	hasID := strings.Contains(newTitlePattern, "%id")

	if !hasVariablePart && !hasID {
		for _, e := range episodes {
			result[e.ID] = newTitlePattern
		}
		return result
	}

	var prefix, suffix string
	if hasVariablePart {
		oldTitles := make([]string, len(episodes))
		for i, e := range episodes {
			oldTitles[i] = e.Title
		}
		prefix, suffix = longestCommonPrefixAndSuffix(oldTitles)
	}

	// pad id with zeros to have the same length as the longest id
	maxIDLength := 0
	if hasID {
		for _, e := range episodes {
			if len(e.ID) > maxIDLength {
				maxIDLength = len(e.ID)
			}
		}
	}

	for _, e := range episodes {
		newTitle := newTitlePattern
		if hasVariablePart {
			variablePart := strings.TrimSuffix(strings.TrimPrefix(e.Title, prefix), suffix)
			newTitle = strings.Replace(newTitle, "%v", variablePart, -1)
		}
		if hasID {
			newID := e.ID
			for len(newID) < maxIDLength {
				newID = "0" + newID
			}
			newTitle = strings.Replace(newTitle, "%id", newID, -1)
		}
		result[e.ID] = newTitle
	}

	return result
}

func longestCommonPrefixAndSuffix(strs []string) (longestPrefix string, longestSuffix string) {
	if len(strs) < 2 {
		return longestPrefix, longestSuffix
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

	for i := len(first) - 1; i >= 0; i-- {
		if string(last[i]) == string(first[i]) {
			longestSuffix = string(last[i]) + longestSuffix
		} else {
			break
		}
	}

	return longestPrefix, longestSuffix
}
