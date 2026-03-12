.PHONY: build test install clean \
	install-service install-service-macos install-hooks

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

install-service-macos: install
	@if [ "$$(uname)" != "Darwin" ]; then \
		echo "This target is for macOS only"; \
		exit 1; \
	fi
	mkdir -p $(HOME)/Library/LaunchAgents
	sed 's|@BINARY_PATH@|$(HOME)/.local/bin/claude-notify|g; s|@HOME@|$(HOME)|g' \
		install/com.claude-notify.daemon.plist > \
		$(HOME)/Library/LaunchAgents/com.claude-notify.daemon.plist
	launchctl load \
		$(HOME)/Library/LaunchAgents/com.claude-notify.daemon.plist
	@echo "Service installed and started."

install-hooks:
	@echo "Copy install/hooks.json to your Claude Code hooks directory"
