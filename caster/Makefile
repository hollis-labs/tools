# Cerberus builds the runtime artifact with `make build` (build_strategy: make_standard).
.PHONY: build test
build:
	mkdir -p bin
	go build -o ./bin/caster ./cmd/caster

test:
	go test -race -count=1 ./...
