# Copyright (c) (2025-2026), Choreo Labs LLC. (https://choreolabs.com).
#
# Choreo Labs licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

# Constants.
VERSION_FILE=version.txt
BINARY_NAME=cds
REPOSITORY_DIR=config/repository
OUTPUT_DIR=target
BUILD_DIR=$(OUTPUT_DIR)/.build
DIST_DIR=$(OUTPUT_DIR)/dist
DISTRIBUTION_SRC=distribution

# Platform targets for the release distribution, as <os>/<arch> pairs. Every
# dependency is pure Go, so all of them cross-compile with CGO_ENABLED=0 and no
# per-platform build host is needed.
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

# Product name as registered with the update service. It names the packed
# binary (<PACK>/$(PRODUCT_NAME)), the pack directory, its zip and the product
# identity in <PACK>/updates/product.txt.
PRODUCT_NAME ?= cdd

# Variable constants.
VERSION=$(shell cat $(VERSION_FILE))
# Pack and product names carry no leading "v", so strip it.
PACK_VERSION=$(VERSION:v%=%)

# Tools
PROJECT_DIR := $(realpath $(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
PROJECT_BIN_DIR := $(PROJECT_DIR)/bin
TOOL_BIN ?= $(PROJECT_BIN_DIR)/tools
GOLANGCI_LINT ?= $(TOOL_BIN)/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.14.0

$(TOOL_BIN):
	mkdir -p $(TOOL_BIN)

# Default target.
all: clean lint build integration-test

# Clean up build artifacts.
clean:
	rm -rf $(OUTPUT_DIR)

# Compile the binary for the host platform.
#
# Packaging is `dist`, which produces the release packs. This target is the
# fast compile check, and is what the container image builds: it copies
# $(BUILD_DIR)/$(BINARY_NAME) rather than unpacking a zip.
build: _build

LINT_BASE ?= origin/main

lint: golangci-lint
	cd . && $(GOLANGCI_LINT) run --new-from-rev=$(LINT_BASE) ./...

# Lint all the code.
lint-all: golangci-lint
	cd . && $(GOLANGCI_LINT) run ./...

# Run the integration suite against PostgreSQL, which needs Docker.
integration-test:
ifdef test
	TESTCONTAINERS_RYUK_DISABLED=true go test -v ./test/integration -run '$(test)'
else
	TESTCONTAINERS_RYUK_DISABLED=true go test -v ./test/integration
endif

# Run the same integration suite against the inbuilt database, which needs no Docker.
integration-test-sqlite:
ifdef test
	CDS_TEST_DB=sqlite go test -v ./test/integration -run '$(test)'
else
	CDS_TEST_DB=sqlite go test -v ./test/integration
endif

# Run the same integration suite against MySQL, which needs Docker.
integration-test-mysql:
ifdef test
	TESTCONTAINERS_RYUK_DISABLED=true CDS_TEST_DB=mysql go test -v ./test/integration -run '$(test)'
else
	TESTCONTAINERS_RYUK_DISABLED=true CDS_TEST_DB=mysql go test -v ./test/integration
endif

# Run the same integration suite against SQL Server, which needs Docker.
integration-test-mssql:
ifdef test
	TESTCONTAINERS_RYUK_DISABLED=true CDS_TEST_DB=mssql go test -v ./test/integration -run '$(test)'
else
	TESTCONTAINERS_RYUK_DISABLED=true CDS_TEST_DB=mssql go test -v ./test/integration
endif

integration-test-oracle:
ifdef test
	TESTCONTAINERS_RYUK_DISABLED=true CDS_TEST_DB=oracle go test -v ./test/integration -run '$(test)'
else
	TESTCONTAINERS_RYUK_DISABLED=true CDS_TEST_DB=oracle go test -v ./test/integration
endif

# Run the unit tests.
#
# The race detector runs here because the process shares one connection pool
# across every request and every worker.
unit-test:
	go test -race ./internal/... ./dbscripts/...

# Run the unit tests and write the coverage reports to target/coverage/.
COVERAGE_DIR=$(OUTPUT_DIR)/coverage
COVERAGE_EXCLUDE_FILE=.excludecoverage
COVERPKG=$(shell go list ./... | grep -v -E "$$(awk '!/^\#/ && NF {if (n++) printf "|"; printf "%s", $$0}' $(COVERAGE_EXCLUDE_FILE))" | tr '\n' ',' | sed 's/,$$//')
unit-test-coverage:
	mkdir -p $(COVERAGE_DIR)
	go test -race -count=1 -covermode=atomic -coverpkg="$(COVERPKG)" -coverprofile=$(COVERAGE_DIR)/coverage_unit.out ./internal/... ./dbscripts/...
	go tool cover -func=$(COVERAGE_DIR)/coverage_unit.out > $(COVERAGE_DIR)/coverage_unit.txt
	go tool cover -html=$(COVERAGE_DIR)/coverage_unit.out -o $(COVERAGE_DIR)/coverage_unit.html
	@tail -n 1 $(COVERAGE_DIR)/coverage_unit.txt

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

# Build the platform-specific release distributions.
#
# One source revision produces every supported pack, so the executables in a
# release — and in the updates published from it — are all built from the same
# code.
dist: clean-dist
	@for platform in $(PLATFORMS); do \
		$(MAKE) --no-print-directory _dist_one DIST_OS=$${platform%/*} DIST_ARCH=$${platform#*/} || exit 1; \
	done
	@$(MAKE) --no-print-directory _dist_metadata

# Stage and zip one platform pack. Not meant to be called directly.
#
# Pack layout:
#   bin/$(PRODUCT_NAME)              the runtime ($(PRODUCT_NAME).exe on Windows)
#   bin/start.sh                     runs it in the foreground (start.bat on Windows)
#   repository/conf/                 deployment.yaml, and secrets.env with every value empty,
#                                    readable by its owner only
#   repository/database/             where the inbuilt database is created
#   repository/dbscripts/            DDL for operators running an external database
#   repository/resources/security/   the TLS material, defaulting to Identity Server 7.3.0's key pair
#   updates/product.txt              the update-service product identity of this pack
#   version.txt, LICENSE.txt, README.md (the pack's own, not this repository's)
#
# The update tool setup scripts are not built from this source: they are added
# to bin/ when a release is onboarded to WSO2 Updates, which happens outside
# this repository. They resolve ../updates/product.txt relative to themselves,
# which is why updates/ sits at the pack root.
_dist_one:
	@set -e; \
	os=$(DIST_OS); arch=$(DIST_ARCH); \
	pack=$(PRODUCT_NAME)-$$os-$$arch-$(PACK_VERSION); \
	stage=$(DIST_DIR)/$$pack; \
	exe=$(PRODUCT_NAME); launcher=start.sh; \
	if [ "$$os" = windows ]; then exe=$(PRODUCT_NAME).exe; launcher=start.bat; fi; \
	echo "Building $$pack..."; \
	rm -rf $$stage; \
	mkdir -p $$stage/bin $$stage/updates \
		$$stage/repository/database $$stage/repository/dbscripts $$stage/repository/resources/security; \
	CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o $$stage/bin/$$exe ./cmd/cdd; \
	cp -r $(REPOSITORY_DIR)/. $$stage/repository/; \
	$(DISTRIBUTION_SRC)/pack-config.sh $$stage/repository/conf/deployment.yaml; \
	install -m 600 $(DISTRIBUTION_SRC)/pack/secrets.env $$stage/repository/conf/secrets.env; \
	cp dbscripts/*.sql $$stage/repository/dbscripts/; \
	cp $(DISTRIBUTION_SRC)/pack/security-README.md $$stage/repository/resources/security/README.md; \
	$(DISTRIBUTION_SRC)/default-cert.sh $$stage/repository/resources/security; \
	cp LICENSE.txt $(VERSION_FILE) $$stage/; \
	sed 's/@VERSION@/$(VERSION)/g' $(DISTRIBUTION_SRC)/pack/README.md > $$stage/README.md; \
	install -m 755 $(DISTRIBUTION_SRC)/pack/$$launcher $$stage/bin/; \
	printf '%s\n' "$$pack" > $$stage/updates/product.txt; \
	find $$stage -name '.DS_Store' -delete; \
	cd $(DIST_DIR) && COPYFILE_DISABLE=1 zip -rq $$pack.zip $$pack -x '**/.DS_Store' -x '**/__MACOSX/**'; \
	rm -rf $$pack; \
	shasum -a 256 $$pack.zip > $$pack.zip.sha256; \
	if command -v md5sum >/dev/null 2>&1; then md5sum $$pack.zip > $$pack.zip.md5; else md5 -r $$pack.zip > $$pack.zip.md5; fi; \
	echo "  -> $(DIST_DIR)/$$pack.zip"

# Publish the release metadata beside the packs, so installation
# tooling can resolve the right artifact for a target environment without
# unpacking one.
_dist_metadata:
	@PRODUCT_NAME=$(PRODUCT_NAME) PACK_VERSION=$(PACK_VERSION) PLATFORMS="$(PLATFORMS)" \
		$(DISTRIBUTION_SRC)/release-metadata.sh $(DIST_DIR)

# Remove the staged distributions and their zips.
clean-dist:
	rm -rf $(DIST_DIR)

# Check that LICENSE.txt lists exactly the modules the packs link.
check-licenses:
	@PLATFORMS="$(PLATFORMS)" $(DISTRIBUTION_SRC)/check-licenses.sh LICENSE.txt

# Check that every source file carries the license header. BASE_REF also checks
# that files added since that ref carry the current year.
check-license-headers:
	@./scripts/check-license-headers.sh $(BASE_REF)

help:
	@echo "Makefile targets:"
	@echo "  all                        - Clean, build, and test the project."
	@echo "  clean                      - Remove build artifacts."
	@echo "  build                      - Compile the binary for the host platform."
	@echo "  dist                       - Build the release packs into $(DIST_DIR)."
	@echo "  clean-dist                 - Remove the staged distributions and their zips."
	@echo "  check-licenses             - Check that LICENSE.txt lists exactly the modules the packs link."
	@echo "  check-license-headers      - Check that every source file carries the license header."
	@echo "  integration-test           - Run integration tests against PostgreSQL (use test=TestName to filter)."
	@echo "  integration-test-sqlite    - Run integration tests against the inbuilt database (use test=TestName to filter)."
	@echo "  integration-test-mysql     - Run integration tests against MySQL (use test=TestName to filter)."
	@echo "  integration-test-mssql     - Run integration tests against SQL Server (use test=TestName to filter)."
	@echo "  integration-test-oracle    - Run integration tests against Oracle Database (use test=TestName to filter)."
	@echo "  mq-integration-test        - Run message queue integration tests (use test=TestName to filter)."
	@echo "  unit-test                  - Run unit tests with the race detector."
	@echo "  unit-test-coverage         - Run unit tests and write coverage reports to target/coverage."
	@echo "  lint                       - Run golangci-lint."
	@echo "  help                       - Show this help message."

.PHONY: all clean build dist clean-dist check-licenses check-license-headers _dist_one _dist_metadata lint lint-all help integration-test integration-test-sqlite integration-test-mysql integration-test-mssql integration-test-oracle mq-integration-test unit-test unit-test-coverage

.PHONY: go_install_tool golangci-lint

define go_install_tool
	cd /tmp && \
	GOBIN=$(TOOL_BIN) go install $(2)@$(3)
endef

golangci-lint: $(GOLANGCI_LINT)

$(GOLANGCI_LINT): $(TOOL_BIN)
	$(call go_install_tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
