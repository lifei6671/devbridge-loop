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
	resolution, err := resolveIngressScope(headers, pb.Scope{
		Namespace:   "default",
		Environment: "base",
	})
	if err != nil {
		t.Fatalf("expected new scope headers accepted, got err=%v", err)
	}
	scope := resolution.Scope
	if scope.Namespace != "dev" || scope.Environment != "demo" {
		t.Fatalf("unexpected scope: %+v", scope)
	}
	if !resolution.HeaderScopeIsComplete {
		t.Fatalf("expected header scope complete")
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

func TestResolveIngressScopeMarksPartialHeaderAsIncomplete(t *testing.T) {
	t.Parallel()

	headers := make(http.Header)
	headers.Set("X-Bridge-Namespace", "dev")
	resolution, err := resolveIngressScope(headers, pb.Scope{
		Namespace:   "default",
		Environment: "base",
	})
	if err != nil {
		t.Fatalf("expected partial scope header accepted, got err=%v", err)
	}
	if resolution.HeaderScopeIsComplete {
		t.Fatalf("expected partial header marked as incomplete")
	}
	if resolution.Scope.Namespace != "dev" || resolution.Scope.Environment != "base" {
		t.Fatalf("unexpected scope resolution: %+v", resolution.Scope)
	}
}

func TestApplyIngressScopeHeadersOverridesAndClears(t *testing.T) {
	t.Parallel()

	headers := make(http.Header)
	headers.Set("X-Bridge-Namespace", "dev")
	headers.Set("X-Bridge-Environment", "alice")

	applyIngressScopeHeaders(headers, pb.Scope{
		Namespace:   "prod",
		Environment: "main",
	})
	if headers.Get("X-Bridge-Namespace") != "prod" || headers.Get("X-Bridge-Environment") != "main" {
		t.Fatalf("unexpected overwritten scope headers: namespace=%s environment=%s", headers.Get("X-Bridge-Namespace"), headers.Get("X-Bridge-Environment"))
	}

	applyIngressScopeHeaders(headers, pb.Scope{})
	if headers.Get("X-Bridge-Namespace") != "" || headers.Get("X-Bridge-Environment") != "" {
		t.Fatalf("expected scope headers cleared when scope is empty")
	}
}
