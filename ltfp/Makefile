GO ?= go

.PHONY: proto test

proto:
	./scripts/gen_proto.sh

test:
	$(GO) test ./...

