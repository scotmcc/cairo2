SYSTEM_BIN_DIR = /usr/local/bin

.PHONY: build install test lint run clean package help

help:
	@echo "cairo2 Makefile targets:"
	@echo "  make build    build all three binaries to ./bin/"
	@echo "  make install  install binaries to $(SYSTEM_BIN_DIR)"
	@echo "  make test     run all tests"
	@echo "  make lint     run go vet ./..."
	@echo "  make run      build and run ./bin/cairo"
	@echo "  make clean    remove ./bin"

build:
	bash scripts/build.sh

install: build
	@if [ -w "$(SYSTEM_BIN_DIR)" ]; then \
		install -m 0755 ./bin/cairo          "$(SYSTEM_BIN_DIR)/cairo"; \
		install -m 0755 ./bin/cairo-registry "$(SYSTEM_BIN_DIR)/cairo-registry"; \
		install -m 0755 ./bin/cairo-ctl      "$(SYSTEM_BIN_DIR)/cairo-ctl"; \
	else \
		sudo install -m 0755 ./bin/cairo          "$(SYSTEM_BIN_DIR)/cairo"; \
		sudo install -m 0755 ./bin/cairo-registry "$(SYSTEM_BIN_DIR)/cairo-registry"; \
		sudo install -m 0755 ./bin/cairo-ctl      "$(SYSTEM_BIN_DIR)/cairo-ctl"; \
	fi

package:
	bash scripts/packaging/build-packages.sh

test:
	go test ./...

lint:
	go vet ./...

run: build
	./bin/cairo

clean:
	rm -rf ./bin
