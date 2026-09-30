.DEFAULT_GOAL := help

GO ?= go
APP := ./cmd/scrivsync
EXE = $(shell $(GO) env GOEXE)

.PHONY: help build build-macos build-windows build-all test vet check fmt clean

help: ## Показать доступные команды
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  make %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Собрать приложение для текущей платформы в bin/
	$(GO) build -o bin/scrivsync$(EXE) $(APP)

build-macos: ## Собрать bin/scrivsync-macos-arm64 для Mac с Apple Silicon
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -o bin/scrivsync-macos-arm64 $(APP)

build-windows: ## Собрать bin/scrivsync-windows-amd64.exe для Windows
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -o bin/scrivsync-windows-amd64.exe $(APP)

build-all: build-macos build-windows ## Собрать версии для macOS и Windows

test: ## Запустить все тесты
	$(GO) test ./...

vet: ## Проверить код через go vet
	$(GO) vet ./...

check: test vet ## Запустить тесты и проверку кода

fmt: ## Отформатировать Go-код
	$(GO) fmt ./...

clean: ## Удалить собранные исполняемые файлы из bin/
	rm -f bin/scrivsync bin/scrivsync.exe bin/scrivsync-macos-arm64 bin/scrivsync-windows-amd64.exe
