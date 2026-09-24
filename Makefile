BINARY   := focusflow
REPO_DIR := $(shell pwd)

# VERSION is injected into the binary via -ldflags. Defaults to "dev";
# the release workflow overrides this with VERSION=<tag>.
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

# Where `make install` puts the binary. ~/.local/bin is what launchd jobs
# (the dashboard's launchd job) and hooks resolve, so there is exactly one
# installed copy — never a second one in the repo dir that can drift.
PREFIX   ?= $(HOME)/.local
DASH_LABEL := local.focusflow.dashboard

.PHONY: build install uninstall test clean vet race check fmt-check staticcheck cover dashboard app dashboard-test e2e

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) .

test:
	go test ./...

# The dashboard (Node, no dependencies) lives in ./dashboard.
dashboard:
	cd dashboard && node --no-warnings bin/setup.js --launchd

app:
	cd dashboard && python3 bin/make-app.py

# The app's core journeys in headless Chrome against a throwaway sandbox (needs Google Chrome).
e2e:
	cd dashboard && node e2e/run.mjs

dashboard-test:
	cd dashboard && node --test

vet:
	go vet ./...

race:
	go test -race ./...

fmt-check:
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

cover:
	go test -race -coverpkg=./... -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

# check is what CI runs (minus the coverage gate; see `make cover`).
check: fmt-check vet staticcheck race dashboard-test

install: build
	@mkdir -p $(PREFIX)/bin
	@# Atomic replace: a hook or dashboard exec'ing the binary mid-install
	@# sees either the old or the new file, never a torn one.
	cp $(BINARY) $(PREFIX)/bin/$(BINARY).new
	mv -f $(PREFIX)/bin/$(BINARY).new $(PREFIX)/bin/$(BINARY)
	@echo "installed $$($(PREFIX)/bin/$(BINARY) version) -> $(PREFIX)/bin/$(BINARY)"
	@# Refresh the skill + hooks from the freshly installed binary.
	$(PREFIX)/bin/$(BINARY) skill install --force
	@# Restart the dashboard so it picks up the new binary (if installed).
	@if launchctl list $(DASH_LABEL) >/dev/null 2>&1; then \
		launchctl kickstart -k gui/$$(id -u)/$(DASH_LABEL) && echo "restarted $(DASH_LABEL)"; \
	fi
	@# Warn about any other focusflow earlier on PATH that would shadow this one.
	@first=$$(command -v $(BINARY) 2>/dev/null); \
	if [ -n "$$first" ] && [ "$$first" != "$(PREFIX)/bin/$(BINARY)" ]; then \
		echo "WARNING: $$first shadows $(PREFIX)/bin/$(BINARY) on PATH — remove it or fix PATH"; \
	fi
	@case ":$$PATH:" in *":$(PREFIX)/bin:"*) ;; *) echo "NOTE: add $(PREFIX)/bin to PATH in ~/.zshrc";; esac
	@echo "Run 'focusflow init' once if ~/.focusflow does not exist yet."

uninstall:
	./$(BINARY) skill uninstall
	@echo "Skill and hook removed. Remove the PATH line from ~/.zshrc manually if desired."

clean:
	rm -f $(BINARY) coverage.out
