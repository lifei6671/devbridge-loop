package app

import (
	"net/http"
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

func TestResolveIngressScopeWithNewHeaders(t *testing.T) {
	t.Parallel()

	headers := make(http.Header)
	headers.Set("X-Bridge-Namespace", "dev")
	headers.Set("X-Bridge-Environment", "demo")
	scope, err := resolveIngressScope(headers, pb.Scope{
		Namespace:   "default",
		Environment: "base",
	})
	if err != nil {
		t.Fatalf("expected new scope headers accepted, got err=%v", err)
	}
	if scope.Namespace != "dev" || scope.Environment != "demo" {
		t.Fatalf("unexpected scope: %+v", scope)
	}
}

func TestResolveIngressScopeRejectsLegacyHeaders(t *testing.T) {
	t.Parallel()

	headers := make(http.Header)
	headers.Set("X-DevBridge-Namespace", "dev")
	headers.Set("X-DevBridge-Environment", "demo")
	_, err := resolveIngressScope(headers, pb.Scope{
		Namespace:   "default",
		Environment: "base",
	})
	if err == nil {
		t.Fatalf("expected legacy scope headers rejected")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnsupportedLegacyProtocol) {
		t.Fatalf("unexpected error code: got=%s want=%s", ltfperrors.ExtractCode(err), ltfperrors.CodeUnsupportedLegacyProtocol)
	}
}
