BIN_DIR := bin

.PHONY: build build-server build-migrate build-qrgen clean

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

clean:
	rm -rf $(BIN_DIR)
