SCRIPT := bin/workspace-launcher
VERSION := $(shell cat VERSION)
BIN_DIR ?= $(HOME)/.local/bin
DIST_DIR ?= dist
RELEASE_TARGETS ?= linux_amd64 linux_arm64 darwin_amd64 darwin_arm64

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

release-assets:
	rm -rf "$(DIST_DIR)"
	mkdir -p "$(DIST_DIR)"
	version="$(VERSION)"; \
	version_no_v="$${version#v}"; \
	for target in $(RELEASE_TARGETS); do \
		stage_dir="$(DIST_DIR)/stage/$$target"; \
		mkdir -p "$$stage_dir"; \
		install -m 755 "$(SCRIPT)" "$$stage_dir/workspace-launcher"; \
		ln -sf workspace-launcher "$$stage_dir/wl"; \
		install -m 644 LICENSE "$$stage_dir/LICENSE"; \
		install -m 644 README.md "$$stage_dir/README.md"; \
		install -m 644 VERSION "$$stage_dir/VERSION"; \
		tar -C "$$stage_dir" -czf "$(DIST_DIR)/workspace-launcher_$${version_no_v}_$${target}.tar.gz" workspace-launcher wl LICENSE README.md VERSION; \
	done; \
	cd "$(DIST_DIR)" && \
	if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum ./*.tar.gz > checksums.txt; \
	else \
		shasum -a 256 ./*.tar.gz > checksums.txt; \
	fi
	rm -rf "$(DIST_DIR)/stage"

.PHONY: install uninstall version release-assets
