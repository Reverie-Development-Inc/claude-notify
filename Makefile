.PHONY: build test install clean

BINARY := claude-notify
BUILD_DIR := ./build

build:
	go build -o $(BUILD_DIR)/$(BINARY) \
		./cmd/claude-notify

test:
	go test ./... -v -race

install: build
	cp $(BUILD_DIR)/$(BINARY) \
		$(HOME)/.local/bin/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)
