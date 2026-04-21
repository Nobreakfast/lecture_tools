BIN_DIR := bin

.PHONY: build build-server build-migrate build-qrgen clean \
        test test-go test-e2e test-e2e-ui e2e-install

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
