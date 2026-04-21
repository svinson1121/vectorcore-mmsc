APP := mmsc
BIN := bin/$(APP)
VERSION ?= 0.3.2B
CONFIG ?= config.yaml
GOCACHE ?= /tmp/vectorcore-mmsc-gocache
GOMODCACHE ?= /tmp/vectorcore-mmsc-gomodcache
GO := env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go
NPM ?= npm
WEB_DIR := web
LDFLAGS := -X main.appVersion=$(VERSION)

.PHONY: build web-build test run fmt tidy clean

build: web-build
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mmsc

web-build:
	$(NPM) --prefix $(WEB_DIR) run build

test:
	$(GO) test ./...

run: build
	./$(BIN) -c $(CONFIG)

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin
