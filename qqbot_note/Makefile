BINARY := media-agent-plugin-qqbot-notifier
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
OUT := bin/$(BINARY)-$(GOOS)-$(GOARCH)

.PHONY: build test vet clean

build:
	go build -o $(OUT) .
	@echo "built $(OUT)"

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin
