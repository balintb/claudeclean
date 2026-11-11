VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2

.PHONY: build install test fuzz lint check cover clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/claudeclean .

install:
	go install -trimpath -ldflags '$(LDFLAGS)' .

test:
	go test -race -count=1 ./...

fuzz:
	go test ./internal/layout  -run '^$$' -fuzz '^FuzzClassify$$'        -fuzztime 30s
	go test ./internal/session -run '^$$' -fuzz '^FuzzParseTranscript$$' -fuzztime 30s
	go test ./internal/session -run '^$$' -fuzz '^FuzzLineReader$$'      -fuzztime 30s
	go test ./internal/format  -run '^$$' -fuzz '^FuzzPrintable$$'       -fuzztime 30s

lint:
	go vet ./...
	go run $(STATICCHECK) ./...

check: lint test

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

clean:
	rm -rf bin coverage.out
