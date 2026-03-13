GO ?= go

.PHONY: proto test test-unit test-binding test-integration test-parity test-pressure test-compat test-regression verify-release

proto:
	./scripts/gen_proto.sh

test:
	$(GO) test ./...

test-unit:
	$(GO) test ./pb ./validate ./codec ./errors ./negotiation ./session ./consistency ./runtime ./transport -count=1

test-binding:
	$(GO) test ./transport/grpcbinding ./transport/tcpbinding -count=1

test-integration:
	$(GO) test ./examples/interop -count=1

test-parity:
	$(GO) test ./codec ./transport/grpcbinding ./transport/tcpbinding -count=1

test-pressure:
	$(GO) test ./transport/grpcbinding ./transport/tcpbinding -run '^$$' -bench 'Benchmark(GRPCH2|TCP)(TunnelSmallPayload|TunnelLargePayload|TunnelIdle(Deadline|Probe)|BurstRefill)' -benchtime=1x -count=1

test-compat:
	$(GO) test ./validate ./negotiation ./pb/gen/devbridge/loop/v2 -count=1

test-regression: proto test-unit test-binding test-integration test-parity test-pressure test-compat

verify-release:
	./scripts/verify_release.sh
