BINARY  := filex
PASSWD_BINARY := filex-passwd
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_FLAGS := -ldflags "-X main.Version=$(VERSION)"
INSTALL_DIR ?= /usr/local/bin

.PHONY: all build test install install-openrc clean docker

all: build

build:
	mkdir -p bin
	go build $(BUILD_FLAGS) -o bin/$(BINARY) ./cmd/filex
	go build -o bin/$(PASSWD_BINARY) ./cmd/filex-passwd

test:
	go test ./...

install: build
	install -Dm755 bin/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	install -Dm755 bin/$(PASSWD_BINARY) $(INSTALL_DIR)/$(PASSWD_BINARY)

install-openrc:
	install -Dm755 init/filex.openrc /etc/init.d/filex
	install -d -m 0755 /var/log/filex

clean:
	rm -rf bin/

docker:
	docker build -t filex:latest .

lint:
	go vet ./...
