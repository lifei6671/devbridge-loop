GO ?= go

.PHONY: proto test test-unit test-integration test-parity test-compat test-regression verify-release

proto:
	./scripts/gen_proto.sh

test:
	$(GO) test ./...

test-unit:
	$(GO) test ./pb ./validate ./codec ./errors ./negotiation ./session ./consistency

test-integration:
	$(GO) test ./examples/interop -count=1

test-parity:
	$(GO) test ./codec -count=1

test-compat:
	$(GO) test ./validate ./negotiation ./pb/gen/devbridge/loop/v2 -count=1

test-regression: proto test-unit test-integration test-parity test-compat

verify-release:
	./scripts/verify_release.sh
