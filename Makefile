BIN    := agent-sessions
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
TARGET := $(BINDIR)/$(BIN)

GO ?= go

.DEFAULT_GOAL := build

.PHONY: build
build:
	@mkdir -p $(BINDIR)
	$(GO) build -o $(TARGET) .
	@echo "built $(TARGET)"

.PHONY: run
run:
	$(GO) run . $(ARGS)

# The pet is a separate toolchain: SwiftPM, no Xcode project. It is deliberately
# not part of `check`, which must keep working on a machine with no Swift.
.PHONY: pet
pet:
	swift build -c release --package-path overlay
	@echo "built overlay/.build/release/AgentPet"

.PHONY: pet-test
pet-test:
	swift test --package-path overlay

.PHONY: fmt
fmt:
	gofmt -w .

# gofmt -l prints offending filenames and still exits 0, so the list itself has
# to be turned into the failure.
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt'd:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

.PHONY: compile
compile:
	$(GO) build ./...

.PHONY: vet
vet:
	$(GO) vet ./...

# Darwin only: termjump drives iTerm2 through osascript and returns early on any
# other GOOS, so its suite asserts darwin behaviour and fails rather than skips.
.PHONY: test
test:
	$(GO) test -race ./...

# Exits non-zero only for a vulnerability that is actually reached; advisories
# for unreached code are reported without failing.
.PHONY: vuln
vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: check
check: fmt-check compile vet test

.PHONY: ci
ci: check vuln

.PHONY: clean
clean:
	rm -f $(TARGET) ./$(BIN)

# Grype exits 0 on findings unless --fail-on is given, and refuses to run at all
# on a database older than 5 days, so a scan that is never updated turns into a
# silent pass.
GRYPE_FAIL_ON ?= high

.PHONY: grype
grype:
	@grype db update
	grype dir:. --fail-on $(GRYPE_FAIL_ON)

.PHONY: audit
audit: vuln grype
