BIN      := dist/bankai
LINK     := $(HOME)/.local/bin/bankai
PKG      := ./cmd/bankai
GOFLAGS  := -trimpath

.PHONY: all build install run test clean

all: build

build:
	@mkdir -p dist
	go build $(GOFLAGS) -o $(BIN) $(PKG)
	@echo "built $(BIN)"

install: build
	@rm -f $(LINK)
	@mkdir -p $(dir $(LINK))
	ln -s $(abspath $(BIN)) $(LINK)
	@echo "linked $(LINK) -> $(abspath $(BIN))"

run: build
	./$(BIN)

test:
	go test ./...

clean:
	rm -rf dist
