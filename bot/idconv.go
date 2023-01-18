package bot

import (
	"fmt"
	"strconv"
	"strings"
)

// formatIDsCompactly formats IDs in a most compact way possible.
// [1,2,3,4,5,6,7,8,9,10] -> 1_to_10
// [1, 11, 12, 13] -> 1_11_to_13
// [1, 11, 12] -> 1_11_12
func formatIDsCompactly(ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}

	if len(ids) == 1 {
		return ids[0], nil
	}

	if len(ids) == 2 {
		return ids[0] + "_" + ids[1], nil
	}

	parsed := make([]int, len(ids))
	for i, id := range ids {
		asInt, err := strconv.Atoi(id)
		if err != nil {
			return "", fmt.Errorf("failed to parse id %q: %w", id, err)
		}
		parsed[i] = asInt
	}

	var resultParts []string
	rangeStartIdx := 0

	for i := range parsed {
		if i == rangeStartIdx {
			resultParts = append(resultParts, strconv.Itoa(parsed[i]))
		}

		isEnd := i == len(parsed)-1
		isRangeEnd := i < len(parsed)-1 && parsed[i]+1 != parsed[i+1]

		if !isEnd && !isRangeEnd {
			continue
		}

		sinceRangeStart := i - rangeStartIdx
		if sinceRangeStart == 1 {
			resultParts = append(resultParts, strconv.Itoa(parsed[i]))
		} else if sinceRangeStart >= 2 {
			resultParts = append(resultParts, "to")
			resultParts = append(resultParts, strconv.Itoa(parsed[i]))
		}

		rangeStartIdx = i + 1
	}

	return strings.Join(resultParts, "_"), nil
}

// parseIDs parses IDs from a compactly formatted string.
// 1_to_10 -> [1,2,3,4,5,6,7,8,9,10]
// 1_11_to_13 -> [1, 11, 12, 13]
// 1_11_12 -> [1, 11, 12]
func parseIDs(idsStr string) ([]string, error) {
	parts := strings.Split(idsStr, "_")
	var result []int
	for i, p := range parts {
		if p == "to" {
			end, err := strconv.Atoi(parts[i+1])
			if err != nil {
				return nil, fmt.Errorf("failed to parse id %q: %w", p, err)
			}
			for i := result[len(result)-1] + 1; i < end; i++ {
				result = append(result, i)
			}
			continue
		}
		parsed, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse id %q: %w", p, err)
		}
		result = append(result, parsed)
	}

	var resultStr []string
	for _, r := range result {
		resultStr = append(resultStr, strconv.Itoa(r))
	}

	return resultStr, nil
}
