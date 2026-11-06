VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= /usr/local

.PHONY: build universal test install uninstall clean

build:
	rm -f bin/router
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/router ./cmd/router

# One binary for Apple Silicon and Intel Macs (needs lipo, i.e. macOS).
universal:
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/router-arm64 ./cmd/router
	GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/router-amd64 ./cmd/router
	rm -f bin/router
	lipo -create -output bin/router bin/router-arm64 bin/router-amd64
	rm bin/router-arm64 bin/router-amd64

test:
	go vet ./...
	go test -race ./...

# Installs whatever `make build` or `make universal` produced; builds if nothing is there.
install:
	@test -f bin/router || $(MAKE) build
	install -d $(PREFIX)/bin
	install -m 0755 bin/router $(PREFIX)/bin/router

uninstall:
	-$(PREFIX)/bin/router uninstall
	rm -f $(PREFIX)/bin/router

clean:
	rm -rf bin
