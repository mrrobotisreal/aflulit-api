.PHONY: build test vet fmt check linux

build:
	go build -o bin/syncd ./cmd/syncd

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

check: fmt vet test

# Cross-compile for the home server (see deploy/README.md; GOARCH=arm64 for ARM boxes).
linux:
	GOOS=linux GOARCH=amd64 go build -o bin/syncd-linux-amd64 ./cmd/syncd
