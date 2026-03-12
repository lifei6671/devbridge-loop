package negotiation

import (
	"fmt"
	"sort"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// Profile 描述参与方对版本和特性的协商输入。
type Profile struct {
	VersionMajor     uint32
	VersionMinor     uint32
	RequiredFeatures []string
	OptionalFeatures []string
}

// Result 描述协商输出结果。
type Result struct {
	Accepted           bool
	NegotiatedFeatures []string
	MissingRequired    []string
}

// Evaluate 根据本地与远端 profile 计算协商结果。
func Evaluate(local Profile, remote Profile) (Result, error) {
	// 协商双方主版本号必须一致，否则视为不兼容升级。
	if local.VersionMajor == 0 || remote.VersionMajor == 0 {
		return Result{}, ltfperrors.New(ltfperrors.CodeNegotiationInvalidProfile, "version major must be greater than 0")
	}
	if local.VersionMajor != remote.VersionMajor {
		return Result{}, ltfperrors.New(ltfperrors.CodeNegotiationUnsupportedVersion, "version major mismatch")
	}

	localSupported := makeFeatureSet(append(local.RequiredFeatures, local.OptionalFeatures...))
	remoteSupported := makeFeatureSet(append(remote.RequiredFeatures, remote.OptionalFeatures...))
	remoteRequired := normalizeFeatureList(remote.RequiredFeatures)
	missingRequired := make([]string, 0)

	for _, feature := range remoteRequired {
		// required feature 缺失时必须拒绝协商。
		if _, exists := localSupported[feature]; !exists {
			missingRequired = append(missingRequired, feature)
		}
	}
	if len(missingRequired) > 0 {
		return Result{
			Accepted:           false,
			NegotiatedFeatures: nil,
			MissingRequired:    missingRequired,
		}, ltfperrors.New(ltfperrors.CodeNegotiationUnsupportedFeature, fmt.Sprintf("missing required features: %s", strings.Join(missingRequired, ",")))
	}

	negotiatedFeatures := intersectFeatureSet(localSupported, remoteSupported)
	sort.Strings(negotiatedFeatures)
	return Result{
		Accepted:           true,
		NegotiatedFeatures: negotiatedFeatures,
		MissingRequired:    nil,
	}, nil
}

// HasFeature 判断特性列表是否包含指定特性。
func HasFeature(features []string, target string) bool {
	normalizedTarget := strings.TrimSpace(strings.ToLower(target))
	if normalizedTarget == "" {
		return false
	}
	for _, feature := range features {
		// 统一做大小写和空白归一化，避免同义特性重复定义。
		if strings.TrimSpace(strings.ToLower(feature)) == normalizedTarget {
			return true
		}
	}
	return false
}

// normalizeFeatureList 将特性列表归一化并去重。
func normalizeFeatureList(features []string) []string {
	normalized := make([]string, 0, len(features))
	seen := make(map[string]struct{}, len(features))
	for _, feature := range features {
		candidate := strings.TrimSpace(strings.ToLower(feature))
		// 空字符串特性直接丢弃，避免污染协商结果。
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	sort.Strings(normalized)
	return normalized
}

// makeFeatureSet 将列表转换为集合结构供快速查找。
func makeFeatureSet(features []string) map[string]struct{} {
	normalized := normalizeFeatureList(features)
	set := make(map[string]struct{}, len(normalized))
	for _, feature := range normalized {
		// 集合值使用空结构体，避免额外内存占用。
		set[feature] = struct{}{}
	}
	return set
}

// intersectFeatureSet 计算两个特性集合的交集。
func intersectFeatureSet(left map[string]struct{}, right map[string]struct{}) []string {
	intersection := make([]string, 0)
	for feature := range left {
		// 只有双方都支持的特性才能进入协商结果。
		if _, exists := right[feature]; exists {
			intersection = append(intersection, feature)
		}
	}
	return intersection
}
