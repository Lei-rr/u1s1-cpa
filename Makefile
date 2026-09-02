PLUGIN_ID  ?= u1s1
VERSION    ?= $(shell cat VERSION)
BUILD_DIR  ?= dist
GOOS       ?= $(shell go env GOOS)
GOARCH     ?= $(shell go env GOARCH)

EXT_linux   = so
EXT_darwin  = dylib
EXT_windows = dll
EXT = $(or $(EXT_$(GOOS)),so)

LIB     = $(BUILD_DIR)/$(PLUGIN_ID).$(EXT)
ARCHIVE = $(BUILD_DIR)/$(PLUGIN_ID)_$(VERSION)_$(GOOS)_$(GOARCH).zip

.PHONY: all test vet build package clean

all: vet test build

test:
	go test ./...

vet:
	go vet ./...

# CGO is required: the plugin exports the CPA C ABI via cgo.
build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -trimpath -buildmode=c-shared -ldflags '-s -w' -o $(LIB) .
	rm -f $(BUILD_DIR)/$(PLUGIN_ID).h

# The CPA plugin store expects one dynamic library at the archive root.
package: build
	cd $(BUILD_DIR) && zip -q -X $(notdir $(ARCHIVE)) $(PLUGIN_ID).$(EXT)
	cd $(BUILD_DIR) && sha256sum $(notdir $(ARCHIVE)) > $(notdir $(ARCHIVE)).sha256

clean:
	rm -rf $(BUILD_DIR)
