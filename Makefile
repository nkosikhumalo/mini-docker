SHELL := /bin/bash
GO_CACHE ?= /tmp/minidock-go-cache
INTEGRATION_GO_CACHE ?= /tmp/minidock-root-go-cache
SUDO ?= sudo
GO_BIN ?= $(shell command -v go || { test ! -x /usr/local/go/bin/go || echo /usr/local/go/bin/go; })
MAVEN_ARGS ?=

.PHONY: build build-go build-java test test-go test-java test-ask test-integration e2e run-go run-java clean

build: build-go build-java

build-go:
	mkdir -p bin
	cd go-runtime && CGO_ENABLED=0 GOCACHE=$(GO_CACHE) go build -o ../bin/minidockd ./cmd/minidockd
	cd go-runtime && CGO_ENABLED=0 GOCACHE=$(GO_CACHE) go build -o ../bin/minidock-attach ./cmd/minidock
	cd go-runtime && CGO_ENABLED=0 GOCACHE=$(GO_CACHE) go build -o ../bin/minidock-ui ./cmd/minidock-ui

build-java:
	cd java-control-plane && ./mvnw $(MAVEN_ARGS) -DskipTests package

test: test-go test-java test-ask

test-go:
	cd go-runtime && GOCACHE=$(GO_CACHE) go test ./... && GOCACHE=$(GO_CACHE) go vet ./...

test-java:
	cd java-control-plane && ./mvnw $(MAVEN_ARGS) test

test-ask:
	python3 scripts/test-minidock-ask.py
	bash scripts/test-minidock-ask-cli.sh

# Run explicitly: root privileges may be needed for kernel namespaces and mounts.
test-integration:
	$(SUDO) ./scripts/verify-host.sh
	cd go-runtime && $(SUDO) -E env GOCACHE=$(INTEGRATION_GO_CACHE) go test -count=1 -v ./...

# Real namespaces, mounts, cgroups, veth, and firewall setup on a disposable host.
e2e:
	$(SUDO) -E env MINIDOCK_GO_BIN=$(GO_BIN) ./scripts/run-e2e.sh

run-go: build-go
	./bin/minidockd

run-java:
	cd java-control-plane && ./mvnw spring-boot:run

clean:
	rm -f bin/minidockd bin/minidock-attach bin/minidock-ui
	cd java-control-plane && ./mvnw $(MAVEN_ARGS) clean
