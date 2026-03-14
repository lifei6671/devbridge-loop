package adminapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var credentialURIRegex = regexp.MustCompile(`://([^/@:]+):([^@/]+)@`)

var sensitiveKeyMarkers = []string{
	"authorization",
	"cookie",
	"token",
	"secret",
	"password",
	"private_key",
}

// sanitizeExportPayload 对导出载荷做统一脱敏，返回脱敏结果和被处理字段列表。
func sanitizeExportPayload(payload map[string]any) (map[string]any, []string) {
	encodedPayload, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		// 编码失败时返回最小错误对象，避免写接口直接失败暴露内部细节。
		return map[string]any{
			"error": map[string]any{
				"code":    "EXPORT_SANITIZE_FAILED",
				"message": marshalErr.Error(),
			},
		}, []string{"error.message"}
	}
	var genericPayload any
	if unmarshalErr := json.Unmarshal(encodedPayload, &genericPayload); unmarshalErr != nil {
		return map[string]any{
			"error": map[string]any{
				"code":    "EXPORT_SANITIZE_FAILED",
				"message": unmarshalErr.Error(),
			},
		}, []string{"error.message"}
	}
	maskedFields := make(map[string]struct{})
	sanitizedPayload := sanitizeAnyValue(genericPayload, "", maskedFields)
	sanitizedMap, ok := sanitizedPayload.(map[string]any)
	if !ok {
		sanitizedMap = map[string]any{
			"payload": sanitizedPayload,
		}
	}
	maskedFieldList := make([]string, 0, len(maskedFields))
	for field := range maskedFields {
		maskedFieldList = append(maskedFieldList, field)
	}
	sort.Strings(maskedFieldList)
	return sanitizedMap, maskedFieldList
}

// sanitizeAnyValue 递归遍历 map/slice 并按敏感键策略执行脱敏。
func sanitizeAnyValue(rawValue any, keyPath string, maskedFields map[string]struct{}) any {
	switch typedValue := rawValue.(type) {
	case map[string]any:
		sanitized := make(map[string]any, len(typedValue))
		for key, value := range typedValue {
			childPath := joinPath(keyPath, key)
			if shouldMaskFieldByKey(key) {
				sanitized[key] = "******"
				maskedFields[childPath] = struct{}{}
				continue
			}
			sanitized[key] = sanitizeAnyValue(value, childPath, maskedFields)
		}
		return sanitized
	case []any:
		sanitized := make([]any, len(typedValue))
		for index, item := range typedValue {
			itemPath := fmt.Sprintf("%s[%d]", keyPath, index)
			sanitized[index] = sanitizeAnyValue(item, itemPath, maskedFields)
		}
		return sanitized
	case string:
		maskedValue, masked := maskCredentialInString(typedValue)
		if masked {
			maskedFields[keyPath] = struct{}{}
			return maskedValue
		}
		return typedValue
	default:
		return typedValue
	}
}

// shouldMaskFieldByKey 判断字段名是否属于敏感键集合。
func shouldMaskFieldByKey(key string) bool {
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	if normalizedKey == "" {
		return false
	}
	for _, marker := range sensitiveKeyMarkers {
		if strings.Contains(normalizedKey, marker) {
			return true
		}
	}
	return false
}

// maskCredentialInString 对连接串中的 `user:password@` 段做口令脱敏。
func maskCredentialInString(rawValue string) (string, bool) {
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return rawValue, false
	}
	masked := false
	redactedValue := credentialURIRegex.ReplaceAllStringFunc(normalizedValue, func(source string) string {
		segments := credentialURIRegex.FindStringSubmatch(source)
		if len(segments) != 3 {
			return source
		}
		masked = true
		return strings.Replace(source, ":"+segments[2]+"@", ":******@", 1)
	})
	return redactedValue, masked
}

// joinPath 生成 `a.b.c` 风格字段路径，便于输出脱敏字段列表。
func joinPath(parent string, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
