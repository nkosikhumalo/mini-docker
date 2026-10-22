SHELL := /bin/bash
GO_CACHE ?= /tmp/minidock-go-cache
INTEGRATION_GO_CACHE ?= /tmp/minidock-root-go-cache
SUDO ?= sudo

.PHONY: build build-go build-java test test-go test-java test-integration run-go run-java clean

build: build-go build-java

build-go:
	mkdir -p bin
	cd go-runtime && GOCACHE=$(GO_CACHE) go build -o ../bin/minidockd ./cmd/minidockd
	cd go-runtime && GOCACHE=$(GO_CACHE) go build -o ../bin/minidock-attach ./cmd/minidock

build-java:
	cd java-control-plane && ./mvnw -DskipTests package

test: test-go test-java

test-go:
	cd go-runtime && GOCACHE=$(GO_CACHE) go test ./... && GOCACHE=$(GO_CACHE) go vet ./...

test-java:
	cd java-control-plane && ./mvnw test

# Run explicitly: root privileges may be needed for kernel namespaces and mounts.
test-integration:
	$(SUDO) ./scripts/verify-host.sh
	cd go-runtime && $(SUDO) -E env GOCACHE=$(INTEGRATION_GO_CACHE) go test -count=1 -v ./...

run-go: build-go
	./bin/minidockd

run-java:
	cd java-control-plane && ./mvnw spring-boot:run

clean:
	rm -f bin/minidockd bin/minidock-attach
	cd java-control-plane && ./mvnw clean
