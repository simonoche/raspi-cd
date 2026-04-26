.PHONY: build build-server build-agent \
        build-agent-arm64 build-agent-armv7 build-agent-darwin-arm64 build-agent-darwin-amd64 \
        build-server-linux-amd64 build-server-linux-arm64 build-server-darwin-arm64 build-server-darwin-amd64 \
        deb deb-agent deb-server \
        test clean docker-build docker-up docker-down docker-logs

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
DEB_VERSION := $(shell echo $(VERSION) | sed 's/^v//')
LDFLAGS     := -ldflags="-s -w -X main.version=$(VERSION)"
BIN         := bin

build: build-server build-agent

build-server:
	go build $(LDFLAGS) -o $(BIN)/raspicd-server ./cmd/server

build-agent:
	go build $(LDFLAGS) -o $(BIN)/raspicd-agent ./cmd/agent

build-agent-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/raspicd-agent-arm64 ./cmd/agent

build-agent-armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BIN)/raspicd-agent-armv7 ./cmd/agent

build-agent-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/raspicd-agent-darwin-arm64 ./cmd/agent

build-agent-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)/raspicd-agent-darwin-amd64 ./cmd/agent

build-server-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)/raspicd-server-linux-amd64 ./cmd/server

build-server-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/raspicd-server-linux-arm64 ./cmd/server

build-server-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/raspicd-server-darwin-arm64 ./cmd/server

build-server-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)/raspicd-server-darwin-amd64 ./cmd/server

deb: deb-agent deb-server

# Requires nfpm: https://nfpm.goreleaser.com/install/
deb-agent:
	@mkdir -p $(BIN)
	GOOS=linux GOARCH=amd64       go build $(LDFLAGS) -o $(BIN)/raspicd-agent-linux-amd64  ./cmd/agent
	GOOS=linux GOARCH=arm64       go build $(LDFLAGS) -o $(BIN)/raspicd-agent-linux-arm64  ./cmd/agent
	GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BIN)/raspicd-agent-linux-armv7  ./cmd/agent
	BINARY_ARCH=amd64 VERSION=$(DEB_VERSION) nfpm pkg --config nfpm-agent.yml --packager deb --arch amd64 --target $(BIN)/
	BINARY_ARCH=arm64 VERSION=$(DEB_VERSION) nfpm pkg --config nfpm-agent.yml --packager deb --arch arm64 --target $(BIN)/
	BINARY_ARCH=armv7 VERSION=$(DEB_VERSION) nfpm pkg --config nfpm-agent.yml --packager deb --arch armhf --target $(BIN)/

deb-server:
	@mkdir -p $(BIN)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)/raspicd-server-linux-amd64  ./cmd/server
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/raspicd-server-linux-arm64  ./cmd/server
	BINARY_ARCH=amd64 VERSION=$(DEB_VERSION) nfpm pkg --config nfpm-server.yml --packager deb --arch amd64 --target $(BIN)/
	BINARY_ARCH=arm64 VERSION=$(DEB_VERSION) nfpm pkg --config nfpm-server.yml --packager deb --arch arm64 --target $(BIN)/

test:
	go test -v ./...

clean:
	rm -rf $(BIN)/

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f
