package app

import "strings"

// normalizeNonEmptyStringSlice 对字符串数组做 trim，并过滤空值项。
func normalizeNonEmptyStringSlice(rawItems []string) []string {
	if len(rawItems) == 0 {
		return nil
	}
	normalizedItems := make([]string, 0, len(rawItems))
	for _, rawItem := range rawItems {
		normalizedItem := strings.TrimSpace(rawItem)
		if normalizedItem == "" {
			continue
		}
		normalizedItems = append(normalizedItems, normalizedItem)
	}
	return normalizedItems
}
