APP_NAME := pi-client
SRC_DIR  := .
BUILD_DIR := build

all: build-arm64

build-arm64:
	@echo "Cross-compiling for ARM64..."
	GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(APP_NAME)-arm64 $(SRC_DIR)

clean:
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR)

.PHONY: all build-arm64 clean
