SHELL := /bin/bash
VERSION := $(shell tr -d '[:space:]' < VERSION)
IMAGE := kkiit:v$(VERSION)
GO_PACKAGES := ./cmd/... ./internal/...

.PHONY: dev test test-integration build web docker release check

dev:
	go run -ldflags "-X main.version=$(VERSION)" ./cmd/kkiit

test:
	go test $(GO_PACKAGES)

# Needs a throwaway PostgreSQL. The tests create accounts and orders and do not
# clean up, so never point this at an operational database.
#   make test-integration KKIIT_TEST_DSN=postgres://user:pass@host:5432/kkiit_test
test-integration:
	KKIIT_TEST_DSN=$(KKIIT_TEST_DSN) go test ./internal/httpapi/ -run Integration -count=1 -timeout 300s

web:
	cd web && npm ci && npm run build

build: web
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/kkiit ./cmd/kkiit

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

release: docker
	mkdir -p release
	docker save $(IMAGE) | gzip -9 > release/kkiit-v$(VERSION).tar.gz

check:
	go test $(GO_PACKAGES)
	go vet $(GO_PACKAGES)
	cd web && npm ci --ignore-scripts && npm run lint && npm test && npm run build
