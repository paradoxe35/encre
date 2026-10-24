# =============================================================================
# Makefile for Encre - AI-Powered Text Revision Tool
# =============================================================================
#
# Encre is built with Go (Fyne UI) + Rust FFI (rdev, arboard, enigo)
# This Makefile handles cross-platform builds with static linking.
#
# Quick Start:
#   make dev          - Development mode with hot reload
#   make build        - Build for current platform
#   make help         - Show all available commands
#
# =============================================================================

.PHONY: build run clean test install-deps help examples bundle-assets ensure-fyne
.PHONY: build-rust build-rust-linux build-rust-darwin build-rust-darwin-amd64 build-rust-darwin-arm64 build-rust-windows
.PHONY: build-go package-all lint fmt update-deps dev dev-quick
.PHONY: clean-rust clean-go ci-setup ci-build ci-test ci-package verify-static

# fyne takes x.y.z only: the nearest release tag without its v, as the release build does.
ifndef VERSION
    VERSION := $(patsubst v%,%,$(shell git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || echo 0.0.0))
endif
BUILD_NUMBER := 1

# Detect current OS and architecture
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

ifeq ($(UNAME_S),Linux)
    CURRENT_OS := linux
    ifeq ($(UNAME_M),aarch64)
        CURRENT_ARCH := arm64
        RUST_CPU := aarch64
    else
        CURRENT_ARCH := amd64
        RUST_CPU := x86_64
    endif
    # Use gnu target for development (faster, no cross-compile issues)
    # Use musl target for release builds (static linking)
    ifdef STATIC
        RUST_TARGET := $(RUST_CPU)-unknown-linux-musl
        CC := musl-gcc
        EXTRA_LDFLAGS := -linkmode external -extldflags '-static'
    else
        RUST_TARGET := $(RUST_CPU)-unknown-linux-gnu
        CC := gcc
        EXTRA_LDFLAGS :=
    endif
    LIB_EXT := a
    BIN_EXT :=
endif

ifeq ($(UNAME_S),Darwin)
    CURRENT_OS := darwin
    # Detect macOS architecture
    ifeq ($(UNAME_M),arm64)
        CURRENT_ARCH := arm64
        RUST_TARGET := aarch64-apple-darwin
        GO_ARCH := arm64
    else
        CURRENT_ARCH := amd64
        RUST_TARGET := x86_64-apple-darwin
        GO_ARCH := amd64
    endif
    LIB_EXT := a
    BIN_EXT :=
    CC := clang
    EXTRA_LDFLAGS :=
endif

ifeq ($(OS),Windows_NT)
    CURRENT_OS := windows
    CURRENT_ARCH := amd64
    RUST_TARGET := x86_64-pc-windows-gnu
    LIB_EXT := a
    BIN_EXT := .exe
    CC := x86_64-w64-mingw32-gcc
    EXTRA_LDFLAGS := -H windowsgui -linkmode external -extldflags '-static'
endif

# Directories
RUST_FFI_DIR := rust-ffi
LIB_DIR := lib
BIN_DIR := bin

RUST_BUILD := bash scripts/build-rust-ffi.sh

# ============================================================================
# Default target
# ============================================================================
all: build

# ============================================================================
# Help
# ============================================================================
help:
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo "Encre Makefile - Rust FFI + Go Static Build"
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo ""
	@echo "📦 Current Environment:"
	@echo "   Platform:      $(CURRENT_OS)-$(CURRENT_ARCH)"
	@echo "   Rust Target:   $(RUST_TARGET)"
	@echo "   Version:       $(VERSION)"
	@echo ""
	@echo "🚀 Common Commands:"
	@echo "   make dev                  - Run in development mode with hot reload"
	@echo "   make dev-quick            - Run in development mode (no hot reload)"
	@echo "   make build                - Build Rust FFI + Go app for current platform"
	@echo "   make run                  - Build and run application"
	@echo "   make clean                - Clean all build artifacts"
	@echo ""
	@echo "🔧 Build Commands:"
	@echo "   make build-rust           - Build Rust FFI static library (current arch)"
	@echo "   make build-rust-linux     - Build Rust FFI for Linux (musl static)"
	@echo "   make build-rust-darwin    - Build Rust FFI for macOS (auto-detect arch)"
	@echo "   make build-rust-darwin-amd64 - Build Rust FFI for macOS Intel"
	@echo "   make build-rust-darwin-arm64 - Build Rust FFI for macOS Apple Silicon"
	@echo "   make build-rust-windows   - Build Rust FFI for Windows (MinGW)"
	@echo "   make build-go             - Build Go application (requires Rust library)"
	@echo "   make package-all          - Build packages for all platforms"
	@echo ""
	@echo "🧹 Clean Commands:"
	@echo "   make clean                - Clean all build artifacts"
	@echo "   make clean-rust           - Clean Rust artifacts only"
	@echo "   make clean-go             - Clean Go artifacts only"
	@echo ""
	@echo "🧪 Testing & Quality:"
	@echo "   make test                 - Run all tests (Rust + Go)"
	@echo "   make test-rust            - Run Rust tests only"
	@echo "   make test-go              - Run Go tests only"
	@echo "   make lint                 - Run linters (Rust clippy + Go golangci-lint)"
	@echo "   make fmt                  - Format code (Rust + Go)"
	@echo "   make verify-static        - Verify binary is statically linked"
	@echo ""
	@echo "📚 Utilities:"
	@echo "   make install-deps         - Install build dependencies"
	@echo "   make update-deps          - Update all dependencies"
	@echo "   make install              - Install binary to /usr/local/bin"
	@echo "   make examples             - Show detailed usage examples"
	@echo ""
	@echo "🔄 CI/CD:"
	@echo "   make ci-setup             - Setup CI environment"
	@echo "   make ci-build             - CI build"
	@echo "   make ci-test              - CI test"
	@echo "   make ci-package           - CI package"
	@echo ""
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo "💡 Tip: Run 'make examples' for detailed usage examples"
	@echo "════════════════════════════════════════════════════════════════════════════"

# ============================================================================
# Install Dependencies
# ============================================================================
install-deps:
	@echo "Checking build dependencies for $(CURRENT_OS)..."
	@command -v go >/dev/null 2>&1 || { echo "Missing: go"; exit 1; }
	@command -v rustc >/dev/null 2>&1 || { echo "Missing: rustc"; exit 1; }
	@command -v cargo >/dev/null 2>&1 || { echo "Missing: cargo"; exit 1; }
ifeq ($(CURRENT_OS),linux)
	@command -v cmake >/dev/null 2>&1 || { echo "Missing: cmake"; exit 1; }
	@command -v pkg-config >/dev/null 2>&1 || { echo "Missing: pkg-config"; exit 1; }
	@pkg-config --exists alsa || { echo "Missing ALSA development files: libasound2-dev"; exit 1; }
	@test -f /usr/include/wayland-client-core.h || { echo "Missing Wayland development files: libwayland-dev"; exit 1; }
endif
ifeq ($(CURRENT_OS),linux)
ifdef STATIC
	@command -v musl-gcc >/dev/null 2>&1 || { \
		echo "Missing musl-gcc. Install it with: sudo apt-get install musl-tools"; \
		exit 1; \
	}
endif
endif
ifeq ($(CURRENT_OS),darwin)
	@echo "Checking for Xcode Command Line Tools..."
	@xcode-select -p >/dev/null 2>&1 || { \
		echo "Please install Xcode Command Line Tools:"; \
		echo "  xcode-select --install"; \
	}
endif
	@echo "Build dependencies are available."

# ============================================================================
# Build Rust FFI Static Library
# ============================================================================

# Build for current platform
build-rust:
	@echo "Building Rust FFI static library for $(CURRENT_OS)..."
	@mkdir -p $(LIB_DIR)
	RUSTFLAGS="-C target-feature=+crt-static" \
		$(RUST_BUILD) $(RUST_TARGET)
	@echo "Copying static library to $(LIB_DIR)..."
	cp $(RUST_FFI_DIR)/target/$(RUST_TARGET)/release/libencre_ffi.$(LIB_EXT) $(LIB_DIR)/
	@echo "Copying C header bindings..."
	test -f $(RUST_FFI_DIR)/bindings.h && cp $(RUST_FFI_DIR)/bindings.h $(RUST_FFI_DIR)/ || true
	@echo "Rust FFI library built successfully!"

# Build for Linux (musl for static binary)
build-rust-linux:
	@echo "Building Rust FFI static library for Linux (musl)..."
	@mkdir -p $(LIB_DIR)
	rustup target add x86_64-unknown-linux-musl
	RUSTFLAGS="-C target-feature=+crt-static" \
		$(RUST_BUILD) x86_64-unknown-linux-musl
	cp $(RUST_FFI_DIR)/target/x86_64-unknown-linux-musl/release/libencre_ffi.a $(LIB_DIR)/
	@echo "Linux Rust FFI library built!"

# Build for macOS (current architecture)
build-rust-darwin:
	@echo "Building Rust FFI static library for macOS ($(CURRENT_ARCH))..."
	@mkdir -p $(LIB_DIR)
	rustup target add $(RUST_TARGET)
	$(RUST_BUILD) $(RUST_TARGET)
	cp $(RUST_FFI_DIR)/target/$(RUST_TARGET)/release/libencre_ffi.a $(LIB_DIR)/
	@echo "macOS Rust FFI library built for $(CURRENT_ARCH)!"

# Build for macOS Intel (x86_64)
build-rust-darwin-amd64:
	@echo "Building Rust FFI static library for macOS Intel (x86_64)..."
	@mkdir -p $(LIB_DIR)
	rustup target add x86_64-apple-darwin
	$(RUST_BUILD) x86_64-apple-darwin
	cp $(RUST_FFI_DIR)/target/x86_64-apple-darwin/release/libencre_ffi.a $(LIB_DIR)/
	@echo "macOS Intel Rust FFI library built!"

# Build for macOS Apple Silicon (ARM64)
build-rust-darwin-arm64:
	@echo "Building Rust FFI static library for macOS Apple Silicon (ARM64)..."
	@mkdir -p $(LIB_DIR)
	rustup target add aarch64-apple-darwin
	$(RUST_BUILD) aarch64-apple-darwin
	cp $(RUST_FFI_DIR)/target/aarch64-apple-darwin/release/libencre_ffi.a $(LIB_DIR)/
	@echo "macOS Apple Silicon Rust FFI library built!"

# Build for Windows (MinGW)
build-rust-windows:
	@echo "Building Rust FFI static library for Windows (MinGW)..."
	@mkdir -p $(LIB_DIR)
	rustup target add x86_64-pc-windows-gnu
	RUSTFLAGS="-C target-feature=+crt-static" \
		$(RUST_BUILD) x86_64-pc-windows-gnu
	cp $(RUST_FFI_DIR)/target/x86_64-pc-windows-gnu/release/libencre_ffi.a $(LIB_DIR)/
	@echo "Windows Rust FFI library built!"

# ============================================================================
# Resource Bundling
# ============================================================================
bundle-assets: ensure-fyne
	@echo "Embedding UI assets..."
	fyne bundle --package main --output bundled.go assets/icon.png

ensure-fyne:
	@command -v fyne >/dev/null 2>&1 || { \
		echo "Missing Fyne CLI. Install it with: go install fyne.io/tools/cmd/fyne@v1.7.2"; \
		exit 1; \
	}

# fyne packs the binary under encre/usr/local/bin inside Encre.tar.xz.
define extract-linux-binary
	set -e; test -f Encre.tar.xz || { echo "Error: fyne produced no Encre.tar.xz"; exit 1; }; \
	tmp=$$(mktemp -d); tar -xf Encre.tar.xz -C "$$tmp"; \
	binary=$$(find "$$tmp" -path '*/usr/local/bin/*' -type f -perm -u+x | head -n 1); \
	if [ -z "$$binary" ]; then rm -rf "$$tmp"; echo "Error: no binary inside Encre.tar.xz"; exit 1; fi; \
	mkdir -p $(dir $(1)); cp "$$binary" $(1); rm -rf "$$tmp" Encre.tar.xz
endef

# macOS kills an app that opens the microphone without a usage description; the release build adds the same keys.
define finish-app-bundle
	/usr/libexec/PlistBuddy -c "Add :CFBundleDisplayName string Encre" $(1)/Contents/Info.plist 2>/dev/null || true; \
	/usr/libexec/PlistBuddy -c "Add :NSMicrophoneUsageDescription string Encre listens to the microphone while you hold the dictate shortcut." $(1)/Contents/Info.plist 2>/dev/null || true; \
	/usr/libexec/PlistBuddy -c "Add :NSAppleEventsUsageDescription string Encre uses System Events to add itself to your login items." $(1)/Contents/Info.plist 2>/dev/null || true
endef

# ============================================================================
# Build Go Application
# ============================================================================
build-go: bundle-assets ensure-fyne
	@echo "Building Go application with Fyne for $(CURRENT_OS)..."
	@mkdir -p $(BIN_DIR)
	@test -f $(LIB_DIR)/libencre_ffi.a || { \
		echo "Error: Rust FFI library not found. Run 'make build-rust' first."; \
		exit 1; \
	}
	CGO_ENABLED=1 \
	CC=$(CC) \
	fyne package --release \
		--app-version "$(VERSION)" \
		--app-build "$(BUILD_NUMBER)"
	@# Extract the built binary from the package
ifeq ($(CURRENT_OS),linux)
	@$(call extract-linux-binary,$(BIN_DIR)/encre$(BIN_EXT))
endif
ifeq ($(CURRENT_OS),darwin)
	@$(call finish-app-bundle,Encre.app)
	@mkdir -p $(BIN_DIR)
	@rm -rf $(BIN_DIR)/Encre.app
	@mv Encre.app $(BIN_DIR)/Encre.app
endif
ifeq ($(CURRENT_OS),windows)
	@if [ -f Encre.exe ]; then \
		mkdir -p $(BIN_DIR); \
		mv Encre.exe $(BIN_DIR)/encre$(BIN_EXT); \
	fi
endif
	@echo "Go application built successfully!"
	@echo "Binary: $(BIN_DIR)/encre$(BIN_EXT)"
	@echo "Version: $(VERSION) (Build: $(BUILD_NUMBER))"

# Build everything (Rust + Go)
build: build-rust build-go
	@echo ""
	@echo "✓ Build complete!"
	@echo "  Binary: $(BIN_DIR)/encre$(BIN_EXT)"
	@echo ""
	@echo "Run with: ./$(BIN_DIR)/encre$(BIN_EXT)"

# ============================================================================
# Run
# ============================================================================
run: build
	@echo "Running Encre..."
ifeq ($(CURRENT_OS),darwin)
	open -W $(BIN_DIR)/Encre.app
else
	./$(BIN_DIR)/encre$(BIN_EXT)
endif

# ============================================================================
# Testing
# ============================================================================
test: test-rust test-go

test-rust:
	@echo "Running Rust tests..."
	cd $(RUST_FFI_DIR) && cargo test --release

# The Go packages link the Rust library, and the clipboard and window tests need a display.
test-go: build-rust
	@echo "Running Go tests..."
	@if [ -z "$$DISPLAY" ] && [ "$(CURRENT_OS)" = linux ] && command -v xvfb-run >/dev/null 2>&1; then \
		CGO_ENABLED=1 xvfb-run -a go test ./...; \
	else \
		CGO_ENABLED=1 go test ./...; \
	fi

# ============================================================================
# Packaging for All Platforms
# ============================================================================
package-all: clean
	@echo "Building for all platforms with Fyne..."
	@mkdir -p $(BIN_DIR)
	$(MAKE) ensure-fyne

	@echo ""
	@echo "=== Building for Linux ==="
	$(MAKE) build-rust-linux
	CGO_ENABLED=1 \
		CC=musl-gcc \
		GOOS=linux \
		GOARCH=amd64 \
		fyne package --release \
			--app-version "$(VERSION)" \
			--app-build "$(BUILD_NUMBER)"
	@$(call extract-linux-binary,$(BIN_DIR)/encre-linux-amd64)

	@echo ""
	@echo "=== Building for macOS (Intel) ==="
	$(MAKE) build-rust-darwin-amd64
	CGO_ENABLED=1 \
		GOOS=darwin \
		GOARCH=amd64 \
		fyne package --icon assets/icon_1024.png --name Encre --app-id me.pngwasi.encre --release \
			--app-version "$(VERSION)" \
			--app-build "$(BUILD_NUMBER)"
	@$(call finish-app-bundle,Encre.app)
	rm -rf $(BIN_DIR)/Encre-darwin-amd64.app
	mv Encre.app $(BIN_DIR)/Encre-darwin-amd64.app

	@echo ""
	@echo "=== Building for macOS (Apple Silicon) ==="
	$(MAKE) build-rust-darwin-arm64
	CGO_ENABLED=1 \
		GOOS=darwin \
		GOARCH=arm64 \
		fyne package --icon assets/icon_1024.png --name Encre --app-id me.pngwasi.encre --release \
			--app-version "$(VERSION)" \
			--app-build "$(BUILD_NUMBER)"
	@$(call finish-app-bundle,Encre.app)
	rm -rf $(BIN_DIR)/Encre-darwin-arm64.app
	mv Encre.app $(BIN_DIR)/Encre-darwin-arm64.app

	@echo ""
	@echo "=== Building for Windows ==="
	$(MAKE) build-rust-windows
	CGO_ENABLED=1 \
		CC=x86_64-w64-mingw32-gcc \
		GOOS=windows \
		GOARCH=amd64 \
		fyne package --release \
			--app-version "$(VERSION)" \
			--app-build "$(BUILD_NUMBER)"
	@# Move the exe to bin directory
	@if [ -f Encre.exe ]; then \
		mkdir -p $(BIN_DIR); \
		mv Encre.exe $(BIN_DIR)/encre-windows-amd64.exe; \
	fi

	@echo ""
	@echo "✓ All platforms built successfully!"
	@ls -lh $(BIN_DIR)/

# ============================================================================
# Verification
# ============================================================================
verify-static:
	@echo "Verifying static linking..."
ifeq ($(CURRENT_OS),linux)
	@echo "Linux binary dependencies:"
	ldd $(BIN_DIR)/encre$(BIN_EXT) || echo "✓ Statically linked (no dynamic dependencies)"
endif
ifeq ($(CURRENT_OS),darwin)
	@echo "macOS binary dependencies:"
	otool -L $(BIN_DIR)/encre$(BIN_EXT)
	@echo "Note: macOS system frameworks are required (cannot be fully static)"
endif
ifeq ($(CURRENT_OS),windows)
	@echo "Windows binary dependencies:"
	objdump -p $(BIN_DIR)/encre$(BIN_EXT) | grep "DLL Name:" || echo "No DLL dependencies found"
endif

# ============================================================================
# Code Quality
# ============================================================================
lint:
	@echo "Running Rust lints..."
	cd $(RUST_FFI_DIR) && cargo clippy -- -D warnings
	@echo "Running Go lints..."
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "Missing golangci-lint. Install it separately before running make lint."; \
		exit 1; \
	}
	@golangci-lint run

fmt:
	@echo "Formatting Rust code..."
	cd $(RUST_FFI_DIR) && cargo fmt
	@echo "Formatting Go code..."
	go fmt ./...
	gofmt -s -w .

# ============================================================================
# Clean
# ============================================================================
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BIN_DIR)/*
	rm -rf $(LIB_DIR)/*
	rm -rf $(RUST_FFI_DIR)/target
	rm -f $(RUST_FFI_DIR)/bindings.h
	rm -rf build/ dist/
	rm -f *.AppImage *.dmg *.deb
	@echo "Clean complete!"

clean-rust:
	@echo "Cleaning Rust artifacts only..."
	rm -rf $(RUST_FFI_DIR)/target
	rm -rf $(LIB_DIR)/*

clean-go:
	@echo "Cleaning Go artifacts only..."
	rm -rf $(BIN_DIR)/*
	go clean -cache -testcache

# ============================================================================
# Development
# ============================================================================
dev: build-rust
	@echo "Starting development mode..."
	@command -v air >/dev/null 2>&1 || { \
		echo "Missing Air. Install it separately or use make dev-quick."; \
		exit 1; \
	}
	CGO_ENABLED=1 air

# Quick dev run without hot reload
dev-quick: build-rust
	@echo "Running in development mode (no hot reload)..."
	CGO_ENABLED=1 go run .

# Update dependencies
update-deps:
	@echo "Updating Rust dependencies..."
	cd $(RUST_FFI_DIR) && cargo update
	@echo "Updating Go dependencies..."
	go get -u ./...
	go mod tidy

# Install locally (after building)
install: build
	@echo "Installing Encre locally..."
ifeq ($(CURRENT_OS),darwin)
	rm -rf /Applications/Encre.app
	cp -R $(BIN_DIR)/Encre.app /Applications/Encre.app
	@echo "Installed to /Applications/Encre.app"
else
	install -m 755 $(BIN_DIR)/encre$(BIN_EXT) /usr/local/bin/encre
	@echo "Installed to /usr/local/bin/encre"
endif

# ============================================================================
# CI/CD Helpers
# ============================================================================
ci-setup:
	@echo "Setting up CI environment..."
	$(MAKE) install-deps

ci-build:
	@echo "CI build..."
	$(MAKE) build

ci-test:
	@echo "CI test..."
	$(MAKE) test

ci-package:
	@echo "CI package..."
	$(MAKE) package-all

# ============================================================================
# Examples & Documentation
# ============================================================================
.PHONY: examples
examples:
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo "Encre - Common Usage Examples"
	@echo "════════════════════════════════════════════════════════════════════════════"
	@echo ""
	@echo "1️⃣  First Time Setup:"
	@echo "   $$ make install-deps         # Install all dependencies"
	@echo "   $$ make build                # Build for your platform"
	@echo "   $$ make run                  # Run the application"
	@echo ""
	@echo "2️⃣  Development Workflow:"
	@echo "   $$ make dev                  # Start with hot reload (recommended)"
	@echo "   $$ make dev-quick            # Start without hot reload (faster startup)"
	@echo "   # Edit code, save, and see changes automatically"
	@echo ""
	@echo "3️⃣  Building for Release:"
	@echo "   $$ make clean                # Clean previous builds"
	@echo "   $$ make build                # Build optimized binary"
	@echo "   $$ ./bin/encre           # Run the binary"
	@echo ""
	@echo "4️⃣  Cross-Platform Build (macOS example):"
	@echo "   # Build for your Mac (auto-detects Intel or Apple Silicon)"
	@echo "   $$ make build"
	@echo ""
	@echo "   # Build specifically for Intel Mac"
	@echo "   $$ make build-rust-darwin-amd64"
	@echo "   $$ CGO_ENABLED=1 GOARCH=amd64 fyne package --release"
	@echo ""
	@echo "   # Build specifically for Apple Silicon Mac"
	@echo "   $$ make build-rust-darwin-arm64"
	@echo "   $$ CGO_ENABLED=1 GOARCH=arm64 fyne package --release"
	@echo ""
	@echo "5️⃣  Code Quality:"
	@echo "   $$ make fmt                  # Format code"
	@echo "   $$ make lint                 # Run linters"
	@echo "   $$ make test                 # Run all tests"
	@echo ""
	@echo "6️⃣  Deployment:"
	@echo "   $$ make install              # Install to /usr/local/bin"
	@echo "   $$ make package-all          # Build for all platforms"
	@echo ""
	@echo "════════════════════════════════════════════════════════════════════════════"
