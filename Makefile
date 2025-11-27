ROOT_DIR              := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

# Set this to "true" to do releases or push to the development registry
CI			          ?= false
INSTALLATION_PATH     ?= ./bin

UNIT_TEST_DIRS        := ./cmd/... ./internal/... ./providers/...
INTEGRATION_TEST_DIRS := ./test/integration/...
E2E_TEST_DIRS         := ./test/e2e/...


ifneq ($(shell pwd),$(ROOT_DIR))
$(error Not running from repo root; use: cd $(ROOT_DIR))
endif

SHELL := /bin/bash
export PATH := $(abspath $(INSTALLATION_PATH)):$(PATH)

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all                                Run unit tests and build."
	@echo "  clean                              Remove build artifacts."
	@echo "  build                              Build binary."
	@echo "  build-image                        Build docker image for the current host, using the binary of produced by make build."
	@echo "  test                               Run unit tests."
	@echo "  coverage[-unit,-integration,-e2e]  Run the tests collecting coverage data."
	@echo "  coverage-report[-browser,-file]    Display coverage report in the cli, or as an HTML page in the browser or in a file using the appropriate suffix."
	@echo "  coverage-clean                     Remove coverage data and reports."
	@echo "  lint[-diff,-all]                   Run linter for the latest changes with autofix, or only displaying the report ("-diff"). "-all" runs the linter in the entire codebase without autofix."
	@echo "  format[-diff]                      Run formater with autoformat or displaying the diff"
	@echo "  build-and-push-development-images  Create and upload to the development registry images using the current changes.                             (requires CI=true)"
	@echo "  release                            Create or update a release using the latest git tag                                                         (requires CI=true)"
	@echo "  setup                              Setup local tools"
	@echo "  reset                              Remove local tools"


binary                ?= far-edge-kubelet
VERSION               := $(shell git describe --tags --always --dirty="-dev")
DATE                  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
VERSION_FLAGS         := -ldflags='-X "main.buildVersion=$(VERSION)" -X "main.buildTime=$(DATE)"'
.PHONY: build
build: build_tags := netgo osusergo
build: OUTPUT_DIR ?= ./dist/local/bin
build:
	@echo "Building..."
	CGO_ENABLED=0 go build -ldflags '-extldflags "-static"' -o $(OUTPUT_DIR)/$(binary) $(if $V,-v) $(VERSION_FLAGS) ./cmd/$(binary)

.PHONY: build-image
build-image: image-name ?= fhp/far-edge-kubelet
build-image: image-version ?= $(VERSION)
build-image: build
	@mkdir -p ./dist/local/image
	cp ./dist/local/bin/$(binary) ./dist/local/image
	cp ./Dockerfile ./dist/local/image
	cp -r --parents ./deploy/configuration-data ./dist/local/image
	cd ./dist/local/image && docker build -t $(image-name):$(image-version) .

.PHONY: all
all: test build

.PHONY: clean
clean:
	@echo "Clean..."
	rm -rf dist

.PHONY: test test-all test-unit test-integration test-e2e
test test-unit:
	go test $(UNIT_TEST_DIRS)

test-integration:
	go test $(INTEGRATION_TEST_DIRS)

test-e2e:
	go test $(E2E_TEST_DIRS)

test-all: test-unit test-integration test-e2e

.PHONY: release
release: check-ci-env
	goreleaser release

BRANCH := $(shell git branch --show-current | sed -e 's|[^A-Za-z0-9_.-]|-|g' -e 's|^-*||' -e 's|-*$$||' -e 's|--*|-|g')
.PHONY: build-and-push-development-images
build-and-push-development-images: check-ci-env
	Summary=$(VERSION) Branch=$(BRANCH) goreleaser --config .goreleaser-dev.yaml --skip validate --clean

define test_with_coverage
	@bash -c '\
		PROFILE="$(ROOT_DIR)/coverage/$(1).coverprofile"; \
		COVERDIR="$(ROOT_DIR)/coverage/$(1)"; \
		rm -rf $$PROFILE $$COVERDIR; \
		mkdir -p $$COVERDIR; \
		COVERPKG_OPT=""; \
		if [ -n "$(3)" ]; then \
			COVERPKGS_CSV=$$(echo "$(3)" | tr " " ","); \
			COVERPKG_OPT="-coverpkg=$$COVERPKGS_CSV"; \
		fi; \
		TMP_OUTPUT=$$(mktemp); \
		go test $$COVERPKG_OPT -coverprofile=$$PROFILE -covermode=count $(2) -test.gocoverdir=$$COVERDIR 2>&1 | tee $$TMP_OUTPUT; \
		REAL_STATUS=$${PIPESTATUS[0]}; \
		EXIT_STATUS=$$REAL_STATUS; \
		if grep -q "matched no packages" $$TMP_OUTPUT; then \
			echo "[INFO] No packages matched. Treating as success."; \
			EXIT_STATUS=0; \
		fi; \
		if grep -q "FAIL" $$TMP_OUTPUT; then \
			echo "[FAIL] Found failed tests. Treating as failure."; \
			EXIT_STATUS=1; \
		fi; \
		rm -f $$TMP_OUTPUT; \
		exit $$EXIT_STATUS; \
	'
endef

.PHONY: coverage-unit
coverage-unit:
	$(call test_with_coverage,unit,$(UNIT_TEST_DIRS))

.PHONY: coverage-integration
coverage-integration:
	$(call test_with_coverage,integration,$(INTEGRATION_TEST_DIRS),$(UNIT_TEST_DIRS))

.PHONY: coverage-e2e
coverage-e2e:
	$(call test_with_coverage,e2e,$(E2E_TEST_DIRS),$(UNIT_TEST_DIRS))

.PHONY: coverage-clean
coverage-clean:
	rm -rf coverage

.PHONY: coverage
coverage: coverage-clean coverage-unit coverage-integration coverage-e2e

.PHONY: coverage-report-browser
coverage-report-browser:
	go tool covdata textfmt -i ./coverage/unit,./coverage/e2e,./coverage/integration -o ./coverage/merged.coverprofile
	go tool cover -html ./coverage/merged.coverprofile

.PHONY: coverage-report-file
coverage-report-file:
	go tool covdata textfmt -i ./coverage/unit,./coverage/e2e,./coverage/integration -o ./coverage/merged.coverprofile
	go tool cover -html ./coverage/merged.coverprofile -o ./coverage/merged.html

.PHONY: coverage-report
coverage-report:
	go tool covdata textfmt -i ./coverage/unit,./coverage/e2e,./coverage/integration -o ./coverage/merged.coverprofile
	go tool cover -func ./coverage/merged.coverprofile

.PHONY: check-ci-env
ifeq ($(CI),true)
check-ci-env:
	@true
else
check-ci-env:
	$(error CI must be set to 'true' to run this target)
endif

.PHONY: lint lint-and-fix lint-all lint-diff lint-ci
lint lint-and-fix:
	golangci-lint run -n --fix

lint-diff:
	golangci-lint run -n

lint-all:
	golangci-lint run

lint-ci:
	golangci-lint run --new-from-rev=HEAD~

.PHONY: format fmt format-and-fix format-ci format-diff
format fmt format-and-fix:
	golangci-lint fmt

format-ci:
	golangci-lint fmt --diff

format-diff:
	golangci-lint fmt --diff --diff-colored

.PHONY: bump-major bump-minor bump-patch bump-prerelease
bump-major:
	@./scripts/bump-version-and-tag.sh major
bump-minor:
	@./scripts/bump-version-and-tag.sh minor
bump-patch:
	@./scripts/bump-version-and-tag.sh patch
bump-prerelease:
	@./scripts/bump-version-and-tag.sh prerelease

.PHONY: authors
authors: REF ?= origin/development
authors:
	@echo "Generating AUTHORS file from commits after $(REF)..."
	@git fetch
	@MERGE_BASE=$$(git merge-base $(REF) HEAD); \
		git log $$MERGE_BASE..HEAD --pretty=format:'%aN <%aE>' \
		| grep -vE 'stash|not.committed.yet' \
		| sed 's/á/a/g; s/Á/A/g; s/à/a/g; s/À/A/g; s/ã/a/g; s/Ã/A/g; s/â/a/g; s/Â/A/g' \
		| sed 's/é/e/g; s/ê/e/g; s/í/i/g; s/ó/o/g; s/õ/o/g; s/ú/u/g; s/ç/c/g' \
		| sed 's/[^[:print:]]//g' \
		| sort -u \
		> NEWGITAUTHORS

	@cat AUTHORS NEWGITAUTHORS | sort -u > NEWAUTHORS
	@mv NEWAUTHORS AUTHORS
	@rm -f NEWGITAUTHORS
	@echo "AUTHORS file updated."

.PHONY: version
version:
	@echo ${VERSION}

.PHONY: branch
branch:
	@echo ${BRANCH}

.PHONY: reset
reset:
	rm -rf ./bin
	rm -rf ./coverage
	rm -rf ./dist

.PHONY: setup
setup: reset
	mkdir ./bin
	@$(MAKE) install-golangci-lint
	@$(MAKE) install-cosign
	@$(MAKE) install-goreleaser

.PHONY: install-golangci-lint
golangci-lint-version := v2.1.6
install-golangci-lint:
	@command -v $(INSTALLATION_PATH)/golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Installing..."; \
		mkdir -p $(INSTALLATION_PATH); \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(INSTALLATION_PATH) ${golangci-lint-version}; \
	}

.PHONY: install-cosign
cosign-version := v2.5.0
install-cosign:
	@command -v $(INSTALLATION_PATH)/cosign >/dev/null 2>&1 || { \
		echo "cosign not found. Installing..."; \
		mkdir -p $(INSTALLATION_PATH); \
		OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
		ARCH=$$(uname -m); \
		case "$$ARCH" in \
            x86_64) ARCH="amd64" ;; \
			aarch64 | arm64) ARCH="arm64" ;; \
			*) echo "Unsupported architecture: $$ARCH" && exit 1 ;; \
		esac; \
		curl -O -L "https://github.com/sigstore/cosign/releases/download/$(cosign-version)/cosign-$$OS-$$ARCH"; \
		mv cosign-$$OS-$$ARCH $(INSTALLATION_PATH)/cosign; \
		chmod +x $(INSTALLATION_PATH)/cosign; \
	}

.PHONY: install-goreleaser
goreleaser-version := v2.9.0
install-goreleaser: install-cosign
	@command -v $(INSTALLATION_PATH)/goreleaser >/dev/null 2>&1 || { \
	mkdir -p $(INSTALLATION_PATH); \
	OS="$$(uname -s)"; \
	ARCH="$$(uname -m)"; \
	if [ "$$ARCH" = "aarch64" ]; then ARCH="arm64"; fi; \
	TAR_FILE="goreleaser_$${OS}_$${ARCH}.tar.gz"; \
	mkdir ./.tmp; \
	cd ./.tmp; \
	echo "Downloading $$TAR_FILE..."; \
	curl -sfLO "https://github.com/goreleaser/goreleaser/releases/download/${goreleaser-version}/$$TAR_FILE"; \
	curl -sfLO "https://github.com/goreleaser/goreleaser/releases/download/${goreleaser-version}/checksums.txt"; \
	echo "Verifying checksums..."; \
	sha256sum --ignore-missing --quiet --check checksums.txt; \
	if command -v cosign >/dev/null 2>&1; then \
		echo "Verifying signatures..."; \
		REF="refs/tags/${goreleaser-version}"; \
		cosign verify-blob \
			--certificate-identity-regexp "https://github.com/goreleaser/goreleaser.*/.github/workflows/.*.yml@$$REF" \
			--certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
			--cert "https://github.com/goreleaser/goreleaser/releases/download/${goreleaser-version}/checksums.txt.pem" \
			--signature "https://github.com/goreleaser/goreleaser/releases/download/${goreleaser-version}/checksums.txt.sig" \
			checksums.txt; \
	else \
		echo "Could not verify signatures, cosign is not installed."; \
	fi; \
	tar -xf "$$TAR_FILE" -C "../bin"; \
	cd ..; \
	rm -r ./.tmp; \
	}
