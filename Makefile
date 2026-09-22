BINARY := gridcoinresearch-tui
LDFLAGS := -s -w
VERSION ?= dev

.PHONY: build run test lint tidy release clean

go.sum: go.mod
	go mod download

build: go.sum
	go build -ldflags="$(LDFLAGS) -X github.com/gridcat/gridcoinresearch-tui/internal/buildinfo.Version=$(VERSION)" -o $(BINARY) ./cmd/gridcoinresearch-tui

run: build
	./$(BINARY)

test: go.sum
	go test ./...

lint:
	go vet ./...
	gofmt -l . | tee /dev/stderr | (! read)

tidy:
	go mod tidy

release:
	goreleaser release --clean

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -f $(BINARY)
	rm -rf dist/
