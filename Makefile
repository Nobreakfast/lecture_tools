BIN_DIR := bin

.PHONY: build build-server build-migrate build-qrgen build-mcp clean \
        test test-go test-e2e test-e2e-ui e2e-install docs-screenshots

build: build-server build-migrate build-qrgen

build-server:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/server ./cmd/server

build-migrate:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/migrate ./cmd/migrate

build-qrgen:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/qrgen ./cmd/qrgen

build-mcp:
	@mkdir -p $(BIN_DIR)/mcp
	GOOS=darwin GOARCH=arm64 go build -o $(BIN_DIR)/mcp/lecture_tools-darwin-arm64 ./cmd/mcp
	GOOS=darwin GOARCH=amd64 go build -o $(BIN_DIR)/mcp/lecture_tools-darwin-amd64 ./cmd/mcp
	GOOS=windows GOARCH=amd64 go build -o $(BIN_DIR)/mcp/lecture_tools-windows-amd64.exe ./cmd/mcp

run: build-server
	./$(BIN_DIR)/server

clean:
	rm -rf $(BIN_DIR)

# `make -j2 test` runs test-go and test-e2e in parallel.
# The `+` prefix lets sub-makes inside playwright global-setup
# inherit the jobserver (silences "jobserver unavailable" warning).
test: test-go test-e2e

test-go:
	go test ./...

e2e-install:
	@cd e2e && \
	if [ ! -f node_modules/.package-lock.json ]; then \
		rm -rf node_modules && npm install; \
	fi

test-e2e: build-server build-migrate e2e-install
	+cd e2e && npx playwright test

test-e2e-ui: build-server build-migrate e2e-install
	+cd e2e && npx playwright test --ui

docs-screenshots: build-server build-migrate e2e-install
	+cd e2e && npm run docs:screenshots
