.PHONY: build build-server build-agent build-agent-arm64 build-agent-armv7 \
        test clean docker-build docker-up docker-down docker-logs

VERSION  ?= 0.1.0
LDFLAGS  := -ldflags="-s -w -X main.version=$(VERSION)"
BIN      := bin

build: build-server build-agent

build-server:
	go build $(LDFLAGS) -o $(BIN)/raspideploy-server ./cmd/server

build-agent:
	go build $(LDFLAGS) -o $(BIN)/raspideploy-agent ./cmd/agent

build-agent-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/raspideploy-agent-arm64 ./cmd/agent

build-agent-armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BIN)/raspideploy-agent-armv7 ./cmd/agent

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
