# Cerberus builds the runtime artifact with `make build` (build_strategy: make_standard).
.PHONY: build test dist
build:
	mkdir -p bin
	go build -o ./bin/caster ./cmd/caster

test:
	go test -race -count=1 ./...

# Cross-compiled client binaries (pure Go, no cgo), for `scp` to other machines.
dist:
	mkdir -p bin/dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/dist/caster-darwin-arm64 ./cmd/caster
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/dist/caster-darwin-amd64 ./cmd/caster
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/dist/caster-linux-amd64 ./cmd/caster
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/dist/caster-linux-arm64 ./cmd/caster
