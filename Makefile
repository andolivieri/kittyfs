MODULE  := github.com/andolivieri/kittyfs
CMD     := ./cmd/kittyfs
DIST    := dist
VERSION ?= dev
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build build-linux build-windows build-darwin build-darwin-amd64 build-darwin-arm64 build-all vet test clean win mac

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o kittyfs $(CMD)

build-linux:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/kittyfs-linux-amd64 $(CMD)

build-windows:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/kittyfs-windows-amd64.exe $(CMD)

build-darwin-amd64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/kittyfs-darwin-amd64 $(CMD)

build-darwin-arm64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/kittyfs-darwin-arm64 $(CMD)

build-darwin: build-darwin-amd64 build-darwin-arm64

build-all: build-linux build-windows build-darwin

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -rf $(DIST) kittyfs kittyfs.exe

win: build-windows

mac: build-darwin