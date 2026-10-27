# trade_well: build everything into bin/ (git-ignored). `make` with no target builds.
#
# A bare `go build ./cmd/x` writes ./x into the current directory (and for `schwaber` that
# collides with a directory), so build through make or pass -o yourself:
#   go build -o bin/ ./cmd/...        one binary per cmd/<name>, named <name>
#   go build -o bin/ ./cmd/orchestrator
# `go build ./...` alone only checks that everything compiles and writes nothing.

GO      ?= go
BIN_DIR := bin
CMDS    := $(notdir $(wildcard cmd/*))

.PHONY: all build check test vet tidy list clean

all: build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/ ./cmd/...
	@echo "Binaries are in $(BIN_DIR)/"

# check is the pre-finish gate from CLAUDE.md: build, vet and test, no binaries written.
check:
	$(GO) build ./... && $(GO) vet ./... && $(GO) test ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

list:
	@echo $(CMDS) | tr ' ' '\n'

# clean removes built binaries only; bin/.gitkeep stays.
clean:
	find $(BIN_DIR) -mindepth 1 ! -name .gitkeep -delete
