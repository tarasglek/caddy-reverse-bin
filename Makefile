NPROC ?= $(shell getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)
GO_TEST_FLAGS ?= -p $(NPROC)
CADDY_BIN ?= ./caddy
EXAMPLE_DIR := ./examples/reverse-proxy
EXAMPLE_CADDY := ./tmp/caddy
EXAMPLE_ECHO := $(EXAMPLE_DIR)/apps/go-echo/go-echo
EXAMPLE_DETECTOR := $(EXAMPLE_DIR)/detector/example-detector

.PHONY: all documentation lint ok cov tests test test-go test-smoke check static-check build detector-schema detector-schema-check release-dry-run clean example-build example-run example-smoke

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
