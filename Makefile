.PHONY: all build test vet install-skill cross-build clean

BINARY_NAME := fm
VERSION ?= 0.1.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -buildid= -X github.com/ducconit/figma-cli/internal/version.Version=$(VERSION) -X github.com/ducconit/figma-cli/internal/version.Commit=$(COMMIT) -X github.com/ducconit/figma-cli/internal/version.BuildDate=$(BUILD_DATE)
GCFLAGS := all=-l -B -C
BUILD_FLAGS := -trimpath -buildvcs=false -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)"

all: vet test build

build:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY_NAME) ./cmd/fm
	@mkdir -p bin
	@cp -f $(BINARY_NAME) bin/$(BINARY_NAME)

vet:
	go vet ./...

test:
	go test -v -race -count=1 ./...

install-skill: build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o skills/figma-cli/scripts/$(BINARY_NAME) ./cmd/fm
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(BUILD_FLAGS) -o skills/figma-cli/scripts/$(BINARY_NAME)-darwin-arm64 ./cmd/fm
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(BUILD_FLAGS) -o skills/figma-cli/scripts/$(BINARY_NAME).exe ./cmd/fm
	chmod +x skills/figma-cli/scripts/$(BINARY_NAME) skills/figma-cli/scripts/$(BINARY_NAME)-darwin-arm64
	@ln -sf $(BINARY_NAME).exe skills/figma-cli/scripts/$(BINARY_NAME)-windows-amd64.exe
	@ln -sf $(BINARY_NAME) skills/figma-cli/scripts/$(BINARY_NAME)-linux-amd64
	./$(BINARY_NAME) install --path skills --os linux --multi-os

cross-build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(BUILD_FLAGS) -o dist/$(BINARY_NAME)-linux-amd64 ./cmd/fm
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(BUILD_FLAGS) -o dist/$(BINARY_NAME)-linux-arm64 ./cmd/fm
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(BUILD_FLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 ./cmd/fm
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(BUILD_FLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 ./cmd/fm
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(BUILD_FLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe ./cmd/fm
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build $(BUILD_FLAGS) -o dist/$(BINARY_NAME)-windows-arm64.exe ./cmd/fm

clean:
	rm -rf $(BINARY_NAME) bin/ dist/
