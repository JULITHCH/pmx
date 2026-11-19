PREFIX ?= $(HOME)/.local

.PHONY: build install test

build:
	go build -trimpath -ldflags="-s -w" -o pmx .

install: build
	install -Dm755 pmx $(PREFIX)/bin/pmx

test:
	go test ./...
