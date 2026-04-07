APP := mmsc
BIN := bin/$(APP)
CONFIG ?= config.yaml
GOCACHE ?= /tmp/vectorcore-mmsc-gocache
GOMODCACHE ?= /tmp/vectorcore-mmsc-gomodcache
GO := env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go

.PHONY: build test run fmt tidy clean

build:
	@mkdir -p bin
	$(GO) build -o $(BIN) ./cmd/mmsc

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
