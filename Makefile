APP_NAME := connector
BUILD_DIR := ./bin
CMD       := ./cmd/server

.PHONY: all build run dev docker-up docker-down migrate tidy lint clean

all: build

## Compila o binário
build:
	@echo ">> Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -ldflags="-w -s" -o $(BUILD_DIR)/$(APP_NAME) $(CMD)
	@echo ">> Binary: $(BUILD_DIR)/$(APP_NAME)"

## Roda localmente (requer Docker com Postgres+Redis)
run: build
	$(BUILD_DIR)/$(APP_NAME)

## Hot-reload via air (go install github.com/air-verse/air@latest)
dev:
	air -c .air.toml

## Sobe Postgres + Redis em background
docker-up:
	docker compose up -d postgres redis
	@echo ">> Waiting for services..."
	@sleep 3
	@echo ">> Services ready"

## Sobe tudo (inclusive a app)
docker-all:
	docker compose up -d --build

## Para e remove os containers
docker-down:
	docker compose down

## Aplica migrations manualmente
migrate:
	@echo ">> Applying migrations..."
	docker exec -i whatsapp_postgres psql -U $${POSTGRES_USER:-whatsapp} -d $${POSTGRES_DB:-whatsapp_bitrix} < migrations/001_init.sql
	@echo ">> Done"

## Atualiza dependências
tidy:
	go mod tidy

## Lint (requer golangci-lint)
lint:
	golangci-lint run ./...

## Remove binários
clean:
	rm -rf $(BUILD_DIR)

## Exibe ajuda
help:
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
