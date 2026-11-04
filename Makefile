# Constants.
VERSION_FILE=version.txt
BINARY_NAME=cds
REPOSITORY_DIR=config/repository
OUTPUT_DIR=target
BUILD_DIR=$(OUTPUT_DIR)/.build

# Variable constants.
VERSION=$(shell cat $(VERSION_FILE))
# ZIP_FILE_NAME=${BINARY_NAME_PREFIX}-$(VERSION)
PRODUCT_FOLDER=$(BINARY_NAME)-$(VERSION)

# Tools
PROJECT_DIR := $(realpath $(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
PROJECT_BIN_DIR := $(PROJECT_DIR)/bin
TOOL_BIN ?= $(PROJECT_BIN_DIR)/tools
GOLANGCI_LINT ?= $(TOOL_BIN)/golangci-lint
GOLANGCI_LINT_VERSION ?= v1.64.8

$(TOOL_BIN):
	mkdir -p $(TOOL_BIN)

# Default target.
all: clean lint build integration-test

# Clean up build artifacts.
clean:
	rm -rf $(OUTPUT_DIR)

# Build project and package it.
build: _build _package

lint: golangci-lint
	cd . && $(GOLANGCI_LINT) run ./...

# Run the integration suite against PostgreSQL, which needs Docker.
integration-test:
ifdef test
	TESTCONTAINERS_RYUK_DISABLED=true go test -v ./test/integration -run $(test)
else
	TESTCONTAINERS_RYUK_DISABLED=true go test -v ./test/integration
endif

# Run the same integration suite against the inbuilt database, which needs no Docker.
integration-test-sqlite:
ifdef test
	CDS_TEST_DB=sqlite go test -v ./test/integration -run $(test)
else
	CDS_TEST_DB=sqlite go test -v ./test/integration
endif

# Run the unit tests.
#
# The race detector runs here because the process shares one connection pool
# across every request and every worker.
unit-test:
	go test -race ./internal/... ./dbscripts/...

mq-integration-test:
ifdef test
	TESTCONTAINERS_RYUK_DISABLED=true go test -v ./test/activemq_integration/... -run $(test)
else
	TESTCONTAINERS_RYUK_DISABLED=true go test -v ./test/activemq_integration/...
endif

# Build the Go project.
_build:
	mkdir -p $(BUILD_DIR) && \
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/server

# Package the binary and repository directory into a zip file.
_package:
	mkdir -p $(OUTPUT_DIR)/$(PRODUCT_FOLDER) && \
	cp $(BUILD_DIR)/$(BINARY_NAME) $(OUTPUT_DIR)/$(PRODUCT_FOLDER)/ && \
	cp -r $(REPOSITORY_DIR) $(OUTPUT_DIR)/$(PRODUCT_FOLDER)/ && \
	cp $(VERSION_FILE) $(OUTPUT_DIR)/$(PRODUCT_FOLDER)/ && \
	cp -r dbscripts $(OUTPUT_DIR)/$(PRODUCT_FOLDER)/ && \
	cd $(OUTPUT_DIR) && zip -r $(PRODUCT_FOLDER).zip $(PRODUCT_FOLDER) && \
	rm -rf $(PRODUCT_FOLDER) && \
	rm -rf $(BUILD_DIR)

help:
	@echo "Makefile targets:"
	@echo "  all                        - Clean, build, and test the project."
	@echo "  clean                      - Remove build artifacts."
	@echo "  build                      - Build the Go project."
	@echo "  integration-test           - Run integration tests against PostgreSQL (use test=TestName to filter)."
	@echo "  integration-test-sqlite    - Run integration tests against the inbuilt database (use test=TestName to filter)."
	@echo "  mq-integration-test        - Run message queue integration tests (use test=TestName to filter)."
	@echo "  unit-test                  - Run unit tests with the race detector."
	@echo "  lint                       - Run golangci-lint."
	@echo "  help                       - Show this help message."

.PHONY: all clean build lint help integration-test integration-test-sqlite mq-integration-test unit-test

.PHONY: go_install_tool golangci-lint

define go_install_tool
	cd /tmp && \
	GOBIN=$(TOOL_BIN) go install $(2)@$(3)
endef

golangci-lint: $(GOLANGCI_LINT)

$(GOLANGCI_LINT): $(TOOL_BIN)
	$(call go_install_tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
