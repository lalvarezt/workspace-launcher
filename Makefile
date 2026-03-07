SCRIPT := bin/workspace-launcher
VERSION := $(shell cat VERSION)
BIN_DIR ?= $(HOME)/.local/bin

install:
	dest_dir="$${XDG_BIN_HOME:-$(BIN_DIR)}"; \
	mkdir -p "$$dest_dir"; \
	install -m 755 "$(SCRIPT)" "$$dest_dir/workspace-launcher"; \
	ln -sf workspace-launcher "$$dest_dir/wl"; \
	printf 'installed %s (%s) and %s\n' "$$dest_dir/workspace-launcher" "$(VERSION)" "$$dest_dir/wl"

uninstall:
	dest_dir="$${XDG_BIN_HOME:-$(BIN_DIR)}"; \
	rm -f "$$dest_dir/workspace-launcher" "$$dest_dir/wl"

version:
	@printf '%s\n' "$(VERSION)"

.PHONY: install uninstall version
