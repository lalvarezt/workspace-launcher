SCRIPT := bin/workspace-launcher
BENCH_SETUP := scripts/bench-setup
VERSION := $(shell cat VERSION)
BIN_DIR ?= $(HOME)/.local/bin
DIST_DIR ?= dist
RELEASE_TARGETS ?= linux_amd64 linux_arm64 darwin_amd64 darwin_arm64
BENCH_ROOT ?= /tmp/workspace-launcher-bench
BENCH_COUNT ?= 1500
BENCH_ARGS ?=

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
	mkdir -p "$(DIST_DIR)" "$(DIST_DIR)/bin"
	version="$(VERSION)"; \
	version_no_v="$${version#v}"; \
	for target in $(RELEASE_TARGETS); do \
		stage_dir="$(DIST_DIR)/stage/$$target"; \
		mkdir -p "$$stage_dir"; \
		install -m 755 "$(SCRIPT)" "$$stage_dir/workspace-launcher"; \
		install -m 755 "$(SCRIPT)" "$(DIST_DIR)/bin/workspace-launcher_$${version_no_v}_$${target}"; \
		ln -sf workspace-launcher "$$stage_dir/wl"; \
		install -m 644 LICENSE "$$stage_dir/LICENSE"; \
		install -m 644 README.md "$$stage_dir/README.md"; \
		install -m 644 VERSION "$$stage_dir/VERSION"; \
		tar -C "$$stage_dir" -czf "$(DIST_DIR)/workspace-launcher_$${version_no_v}_$${target}.tar.gz" workspace-launcher wl LICENSE README.md VERSION; \
	done; \
	{ \
		cd "$(DIST_DIR)" && \
		find . -maxdepth 2 -type f ! -name checksums.txt -print | LC_ALL=C sort | \
		if command -v sha256sum >/dev/null 2>&1; then \
			xargs sha256sum; \
		else \
			xargs shasum -a 256; \
		fi; \
	} > "$(DIST_DIR)/checksums.txt"
	rm -rf "$(DIST_DIR)/stage"

bench-setup:
	"$(BENCH_SETUP)" --root "$(BENCH_ROOT)" --count "$(BENCH_COUNT)" $(BENCH_ARGS)

.PHONY: install uninstall version release-assets bench-setup
