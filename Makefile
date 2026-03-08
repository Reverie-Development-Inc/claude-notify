.PHONY: build test install clean install-service install-hooks

BINARY := claude-notify
BUILD_DIR := ./build

build:
	go build -o $(BUILD_DIR)/$(BINARY) \
		./cmd/claude-notify

test:
	go test ./... -v -race

install: build
	mkdir -p $(HOME)/.local/bin
	cp $(BUILD_DIR)/$(BINARY) \
		$(HOME)/.local/bin/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)

install-service: install
	mkdir -p $(HOME)/.config/systemd/user
	cp install/claude-notify.service \
		$(HOME)/.config/systemd/user/
	systemctl --user daemon-reload
	systemctl --user enable claude-notify
	@echo "Service installed. Start with:"
	@echo "  systemctl --user start claude-notify"

install-hooks:
	@echo "Copy install/hooks.json to your Claude Code hooks directory"
