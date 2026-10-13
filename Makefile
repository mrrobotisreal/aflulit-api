.PHONY: build test vet fmt check linux linux-arm64

build:
	go build -o bin/syncd ./cmd/syncd

test:
	go test ./... -count=1

vet:
	go vet ./...

# Lists unformatted files (informational). `check` turns that into a failure.
fmt:
	gofmt -l .

# The gate: fails on any unformatted file, vet error or test failure.
check:
	test -z "$$(gofmt -l .)" && go vet ./... && go test ./... -count=1

# Cross-compile for the home server (see deploy/README.md). Pick the arch that
# matches `uname -m` there: x86_64 → linux, aarch64 → linux-arm64.
linux:
	GOOS=linux GOARCH=amd64 go build -o bin/syncd-linux-amd64 ./cmd/syncd

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -o bin/syncd-linux-arm64 ./cmd/syncd
