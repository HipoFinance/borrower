# VERSION is stamped into the binary; `borrower -version` prints it and the log's first line repeats it.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64

.PHONY: help build install test fmt dist clean

help: ## list the targets
	@grep -E '^[a-z]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

# Outputs go under bin/ and dist/ only: the package directory is also called borrower, so a binary
# written to ./borrower -- and a clean that removes it -- would collide with the sources.
build: ## build bin/borrower for this machine
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/borrower .

install: ## install into $(go env GOPATH)/bin
	go install -trimpath -ldflags "$(LDFLAGS)" .

test: ## vet, check formatting, and run the tests
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)
	go test ./...

fmt: ## format the sources
	gofmt -w .

dist: ## cross-compile the release binaries into dist/, with SHA256SUMS
	rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building borrower-$$os-$$arch $(VERSION)"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/borrower-$$os-$$arch . || exit 1; \
	done
	cd dist && sha256sum borrower-* > SHA256SUMS

clean: ## remove build outputs (bin/ and dist/ only)
	rm -rf bin dist
