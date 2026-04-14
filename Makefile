BINARY  := filex
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_FLAGS := -ldflags "-X main.Version=$(VERSION)"
INSTALL_DIR ?= /usr/local/bin

.PHONY: all build test install clean docker

all: build

build:
	mkdir -p bin
	go build $(BUILD_FLAGS) -o bin/$(BINARY) ./cmd/filex

test:
	go test ./...

install:
	go install $(BUILD_FLAGS) ./cmd/filex

clean:
	rm -rf bin/

docker:
	docker build -t filex:latest .

lint:
	go vet ./...
