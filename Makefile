BINARY_NAME := hl_exporter
BUILD_DIR := bin
BUILD_TIME_UTC := $(shell date -u +'%Y-%m-%d_%H:%M:%S')
COMMIT  := $(shell git rev-parse --short HEAD)
VERSION := $(shell cat VERSION)
VERSION_LABEL ?= $(VERSION)

.PHONY: build clean explain

.DEFAULT_GOAL := explain

explain:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Options for test targets:"
	@echo "  [N=...]              - Number of times to run burst tests (default 1)"
	@echo "  [RACE=1]             - Run tests with race detector"
	@echo "  [VERSION_LABEL=...]  - Override version label"
	@echo "  [V=1]                - Add V=1 for verbose output"
	@echo ""
	@echo "Targets:"
	@echo "  build            - Build the binary for the host OS/Arch."
	@echo "  clean            - Clean up the build directory."
	@echo "  fmt              - Format the code."
	@echo "  lint             - Run golangci-lint (configuration in .golangci.yml)."
	@echo "  test             - Run unit tests (RACE=1 for race detector)."
	@echo "  explain          - Display this help message."

# Number of times to run burst tests, default 1
N ?= 1

TEST_FLAGS :=
ifdef RACE
	TEST_FLAGS += -race
endif
ifdef V
	TEST_FLAGS += -v
endif

build:
	@echo "==> Building $(BINARY_NAME)..."
	@go build -ldflags "-X main.buildTimeUTC=$(BUILD_TIME_UTC) -X main.commit=$(COMMIT) -X main.version=$(VERSION_LABEL)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/hl-exporter

clean:
	@echo "Cleaning up..."
	rm -rf $(BUILD_DIR)

fmt:
	@echo "==> Formatting code..."
	@gofmt -s -w .

lint:
	@echo "==> Running golangci-lint..."
	@golangci-lint run --build-tags="heavy"

test:
	@echo "==> Running tests..."
	@$(TEST_ENV) go test -shuffle=on -count=$(N) -tags=$(TAGS) $(TEST_FLAGS) ./...
