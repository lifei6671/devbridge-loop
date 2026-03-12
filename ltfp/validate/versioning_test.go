package validate

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
)

// TestValidateSchemaVersionUsingGoldenCases 验证版本兼容性样例。
func TestValidateSchemaVersionUsingGoldenCases(t *testing.T) {
	t.Parallel()

	support := VersionSupport{
		CurrentMajor: 2,
		CurrentMinor: 1,
		MinMajor:     2,
		MinMinor:     0,
	}
	for _, testCase := range testkit.GoldenVersionCases() {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()

			err := ValidateSchemaVersion(testCase.VersionMajor, testCase.VersionMinor, support)
			if testCase.ShouldAccept && err != nil {
				t.Fatalf("expected accept but got error: %v", err)
			}
			if !testCase.ShouldAccept {
				if err == nil {
					t.Fatalf("expected reject but got nil")
				}
				// 不兼容样例应返回版本不支持错误码。
				if !ltfperrors.IsCode(err, ltfperrors.CodeNegotiationUnsupportedVersion) {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
