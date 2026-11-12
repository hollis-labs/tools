.PHONY: build test vet lint pins scan generate dist

SUBSTRATE := go run github.com/hollis-labs/substrate/cmd/substrate

# Cerberus builds the runtime artifact with `make build` (build_strategy: make_standard).
build:
	mkdir -p bin
	go build -o ./bin/caster ./cmd/caster

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint: vet
	golangci-lint run

# The go.mod pins must match the Substrate BOM; edit the BOM (in Substrate), never go.mod.
pins:
	$(SUBSTRATE) pins check --app .

# Hygiene scan of the tree (secrets, hosts, private paths); run from the repository root.
scan:
	cd .. && $(SUBSTRATE) scan-public --tree

# Regenerate the wiring file after editing cmd/caster/substrate.yaml.
generate:
	$(SUBSTRATE) generate -f cmd/caster/substrate.yaml

# Cross-compiled binaries (pure Go, no cgo), for `scp` to other machines.
dist:
	mkdir -p bin/dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/dist/caster-darwin-arm64 ./cmd/caster
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/dist/caster-darwin-amd64 ./cmd/caster
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/dist/caster-linux-amd64 ./cmd/caster
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/dist/caster-linux-arm64 ./cmd/caster
