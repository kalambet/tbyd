BINARY     := tbyd
CMD        := ./cmd/tbyd
BIN_DIR    := ./bin
VERSION    ?= dev
LDFLAGS    := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build run test lint clean

build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) $(CMD)

run: build
	$(BIN_DIR)/$(BINARY)

test:
	go test ./...

lint:
	@which golangci-lint > /dev/null || (echo "golangci-lint not found. Install: brew install golangci-lint" && exit 1)
	golangci-lint run ./...

clean:
	rm -rf $(BIN_DIR)
