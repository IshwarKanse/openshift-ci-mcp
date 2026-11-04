BINARY := openshift-ci-mcp
ORG := rh-edge-enablement
IMAGE ?= quay.io/$(ORG)/$(BINARY)
VERSION ?= 0.0.0-dev

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build build-all test test-integration lint smoke smoke-container image push clean generate check check-domain check-core check-payload check-jobs check-tests check-prs check-prow check-proxies check-all

build:
	go build -o bin/$(BINARY) ./cmd/$(BINARY)

build-all:
	@for platform in $(PLATFORMS); do \
		os=$${platform%%/*}; arch=$${platform##*/}; \
		echo "Building $$os/$$arch..."; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o bin/$(BINARY)-$$os-$$arch ./cmd/$(BINARY); \
	done

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

lint:
	go vet ./...

image:
	podman build -t $(IMAGE):$(VERSION) -t $(IMAGE):latest -f Containerfile .

push: image
	podman push $(IMAGE):$(VERSION)
	podman push $(IMAGE):latest

smoke: build
	python3 tests/smoke_test.py --binary bin/$(BINARY)

smoke-container:
	python3 tests/smoke_test.py --container $(IMAGE):latest

generate:
	go generate ./pkg/server/...

MCPCHECKER_PARALLEL_TESTS := 4  # The number of tests to allow mcpchecker to run in parallel
MCPCHECKER_TEST_COUNT := 1			# The number of times to run each test
MCPCHECKER_OUTPUT_TYPE = json		# Get JSON, defaults to text

check: check-domain check-proxies

check-domain: mcpchecker build
	$(MCPCHECKER) check mcpchecker/domain-eval.yaml -p $(MCPCHECKER_PARALLEL_TESTS) -n $(MCPCHECKER_TEST_COUNT) -o $(MCPCHECKER_OUTPUT_TYPE)

check-core check-payload check-jobs check-tests check-prs check-prow: check-%: mcpchecker build
	$(MCPCHECKER) check mcpchecker/domain-eval.yaml -l suite=$* -p $(MCPCHECKER_PARALLEL_TESTS) -n $(MCPCHECKER_TEST_COUNT) -o $(MCPCHECKER_OUTPUT_TYPE)

check-proxies: mcpchecker build
	$(MCPCHECKER) check mcpchecker/proxy-eval.yaml -p $(MCPCHECKER_PARALLEL_TESTS) -n $(MCPCHECKER_TEST_COUNT) -o $(MCPCHECKER_OUTPUT_TYPE)

check-all: check

clean:
	rm -rf bin/

verify: test test-integration smoke

# Check for mcpchecker and download if necessary
.PHONY: mcpchecker
MCPCHECKER = ./bin/mcpchecker
mcpchecker:
ifeq (,$(wildcard $(MCPCHECKER)))
	$(call go-get-tool,github.com/mcpchecker/mcpchecker/cmd/mcpchecker@v0.0.18)
endif

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
	@echo "Downloading $(1)"; GOBIN=$(PROJECT_DIR)/bin go install -mod=readonly $(1)
endef
