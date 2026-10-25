NPROC ?= $(shell getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)
GO_TEST_FLAGS ?= -p $(NPROC)
CADDY_BIN ?= ./caddy
EXAMPLE_DIR := ./examples/reverse-proxy
GOVULNCHECK ?= go run golang.org/x/vuln/cmd/govulncheck@latest
PINACT ?= go run github.com/suzuki-shunsuke/pinact/v5/cmd/pinact@latest
NOTES_MAX_WORDS ?= 100
EXAMPLE_CADDY := ./tmp/caddy
EXAMPLE_ECHO := $(EXAMPLE_DIR)/apps/go-echo/go-echo
EXAMPLE_DETECTOR := $(EXAMPLE_DIR)/detector/example-detector

# Fixers rewrite the same files; never run targets in parallel.
.NOTPARALLEL:

.PHONY: all documentation lint ok cov tests test test-go test-smoke check deps-check deps-latest modernize vulncheck release-notes static-check build detector-schema detector-schema-check release-dry-run clean example-build example-run example-smoke

all: ok documentation lint

documentation: doc/index.html doc.go README.md

lint: ok/index.html

ok:
	mkdir -p ok

ok/%.html: doc/%.html | ok
	tidy -quiet -output /dev/null $<
	touch $@

cov: all
	go test $(GO_TEST_FLAGS) -v -coverprofile=coverage ./... && go tool cover -html=coverage -o=coverage.html

tests test: test-go

test-go:
	go test $(GO_TEST_FLAGS) ./...

test-smoke: example-smoke

check: detector-schema-check test-go test-smoke

# Fails only on vulnerabilities reachable from our code.
vulncheck:
	$(GOVULNCHECK) ./...
	cd ./detectorschema && $(GOVULNCHECK) ./...
	cd $(EXAMPLE_DIR) && $(GOVULNCHECK) ./...

# deps-latest and modernize rewrite files; deps-check fails when they did.
# Run it locally and commit the result.
deps-latest:
	go get -u -t ./... && go get toolchain@latest && go mod tidy
	cd ./detectorschema && go get -u -t ./... && go mod tidy
	cd $(EXAMPLE_DIR) && go get -u -t ./... && go mod tidy
	$(PINACT) run -u

modernize:
	go fix ./...
	# detectorschema keeps go 1.24 for its importers, so go fix only applies
	# modernizers up to that language version there.
	cd ./detectorschema && go fix ./...
	cd $(EXAMPLE_DIR) && go fix ./...

deps-check: deps-latest modernize vulncheck
	@if [ -n "$$(git status --porcelain)" ]; then git status --short; git diff; \
	echo "error: fixers changed files; commit them" >&2; exit 1; fi

# Usage: make release-notes TAG=vX.Y.Z
release-notes:
	@test -n "$(TAG)" || { echo "usage: make release-notes TAG=vX.Y.Z" >&2; exit 1; }
	@f=release-notes/$(TAG).md; [ -s "$$f" ] || { echo "error: $$f missing" >&2; exit 1; }; \
	n=$$(wc -w < "$$f"); [ "$$n" -le $(NOTES_MAX_WORDS) ] || { \
	echo "error: $$f: $$n words > $(NOTES_MAX_WORDS). Rewrite caveman: fragments, no filler. Keep what changed, breaking, GO/CVE IDs." >&2; exit 1; }

static-check:
	golint .
	go vet -all .
	gofmt -s -l .
	goreportcard-cli -v

README.md: doc/document.md
	pandoc --read=markdown --write=gfm < $< > $@

doc/index.html: doc/document.md doc/html.txt doc/caddy.xml
	pandoc --read=markdown --write=html --template=doc/html.txt \
		--metadata pagetitle="reverse-bin for Caddy" --syntax-definition=doc/caddy.xml < $< > $@

doc.go: doc/document.md doc/go.awk
	pandoc --read=markdown --write=plain $< | awk --assign=package_name=reversebin --file=doc/go.awk > $@
	gofmt -s -w $@

build:
	go run github.com/caddyserver/xcaddy/cmd/xcaddy@latest build --output $(CADDY_BIN) --with github.com/tarasglek/caddy-reverse-bin=.
	$(CADDY_BIN) list-modules | grep http.handlers.reverse-bin
	$(CADDY_BIN) version

detector-schema:
	mkdir -p schemas
	go run ./cmd/gen-detector-schema > schemas/detector-output.schema.json

detector-schema-check:
	@tmp=$$(mktemp); \
	go run ./cmd/gen-detector-schema > $$tmp; \
	diff -u schemas/detector-output.schema.json $$tmp; \
	rm -f $$tmp

$(EXAMPLE_CADDY):
	mkdir -p $(dir $@)
	go build -o $@ ./cmd/caddy

$(EXAMPLE_ECHO): $(shell find examples/reverse-proxy/apps/go-echo -type f -name '*.go' 2>/dev/null)
	cd $(EXAMPLE_DIR) && go build -o apps/go-echo/go-echo ./apps/go-echo

$(EXAMPLE_DETECTOR): $(shell find examples/reverse-proxy/detector -type f -name '*.go' 2>/dev/null)
	cd $(EXAMPLE_DIR) && go build -o detector/example-detector ./detector

example-build: $(EXAMPLE_CADDY) $(EXAMPLE_ECHO) $(EXAMPLE_DETECTOR)

example-run: example-build
	$(EXAMPLE_CADDY) run --adapter caddyfile --config $(EXAMPLE_DIR)/Caddyfile

example-smoke: example-build
	cd $(EXAMPLE_DIR) && go test $(GO_TEST_FLAGS) -run TestSmoke -count=1

release-dry-run:
	$$(go env GOPATH)/bin/goreleaser release --snapshot --clean --skip=publish

clean:
	rm -rf ./tmp
	rm -f coverage coverage.html ok/* doc/index.html $(CADDY_BIN) $(EXAMPLE_ECHO) $(EXAMPLE_DETECTOR)
