# ═══════════════════════════════════════════════════════════════════════════════
# EthioScript & Voice AI — Makefile
# ═══════════════════════════════════════════════════════════════════════════════

COMPOSE   = docker compose
ENV_FILE  = .env

.PHONY: help up up-infra down down-volumes build rebuild logs \
        logs-account logs-doc logs-job logs-ocr \
        ps clean shell-db shell-rabbit urls migrate

## help: show available targets
help:
	@echo ""
	@echo "  EthioScript & Voice AI — make targets"
	@echo ""
	@grep -E '^## ' Makefile | sed 's/## /    /'
	@echo ""

## up: build + start all services
up:
	$(COMPOSE) --env-file $(ENV_FILE) up -d --build

## up-infra: start only infrastructure (postgres, redis, minio, rabbitmq)
up-infra:
	$(COMPOSE) --env-file $(ENV_FILE) up -d \
	  postgres redis minio rabbitmq minio-init

## up-services: start only microservices (infra must already be running)
up-services:
	$(COMPOSE) --env-file $(ENV_FILE) up -d \
	  account-service document-service job-orchestrator ocr-worker stt-worker

## down: stop all services
down:
	$(COMPOSE) down

## down-volumes: stop and delete all volumes (destructive!)
down-volumes:
	$(COMPOSE) down -v

## build: build all service images without starting
build:
	$(COMPOSE) build

## rebuild: force rebuild (no cache)
rebuild:
	$(COMPOSE) build --no-cache

## logs: tail all logs
logs:
	$(COMPOSE) logs -f

## logs-account: tail account-service logs
logs-account:
	$(COMPOSE) logs -f account-service

## logs-doc: tail document-service logs
logs-doc:
	$(COMPOSE) logs -f document-service

## logs-job: tail job-orchestrator logs
logs-job:
	$(COMPOSE) logs -f job-orchestrator

## logs-ocr: tail ocr-worker logs
logs-ocr:
	$(COMPOSE) logs -f ocr-worker

## ps: show container status
ps:
	$(COMPOSE) ps

## clean: prune stopped containers and dangling images
clean:
	docker system prune -f

## shell-db: open psql inside the postgres container
shell-db:
	$(COMPOSE) exec postgres psql -U $${POSTGRES_USER:-ethio} -d $${POSTGRES_DB:-ethioscript}

## shell-rabbit: show RabbitMQ status
shell-rabbit:
	$(COMPOSE) exec rabbitmq rabbitmqctl status

## migrate: re-run migrations manually
migrate:
	$(COMPOSE) exec postgres psql \
	  -U $${POSTGRES_USER:-ethio} \
	  -d $${POSTGRES_DB:-ethioscript} \
	  -f /docker-entrypoint-initdb.d/001_init_schema.sql
	$(COMPOSE) exec postgres psql \
	  -U $${POSTGRES_USER:-ethio} \
	  -d $${POSTGRES_DB:-ethioscript} \
	  -f /docker-entrypoint-initdb.d/002_account_service_tables.sql

## urls: print all service URLs
urls:
	@echo ""
	@echo "  ╔══════════════════════════════════════════════════════╗"
	@echo "  ║      EthioScript & Voice AI — Service URLs           ║"
	@echo "  ╠══════════════════════════════════════════════════════╣"
	@echo "  ║  SWAGGER UI                                           ║"
	@echo "  ║  Aggregated (all services)                            ║"
	@echo "  ║    → http://localhost:6005                            ║"
	@echo "  ║  Account Service Swagger                              ║"
	@echo "  ║    → http://localhost:6000/docs                       ║"
	@echo "  ║  Document Service Swagger                             ║"
	@echo "  ║    → http://localhost:6001/docs                       ║"
	@echo "  ║  Job Orchestrator Swagger                             ║"
	@echo "  ║    → http://localhost:6002/docs                       ║"
	@echo "  ╠══════════════════════════════════════════════════════╣"
	@echo "  ║  MICROSERVICES                                        ║"
	@echo "  ║  Account Service   → http://localhost:6000            ║"
	@echo "  ║  Document Service  → http://localhost:6001 (internal) ║"
	@echo "  ║  Job Orchestrator  → http://localhost:6002 (internal) ║"
	@echo "  ║  OCR Worker        → http://localhost:6003/metrics    ║"
	@echo "  ║  STT Worker        → http://localhost:6004/metrics    ║"
	@echo "  ╠══════════════════════════════════════════════════════╣"
	@echo "  ║  INFRASTRUCTURE                                       ║"
	@echo "  ║  PostgreSQL        → localhost:6300                   ║"
	@echo "  ║  Redis             → localhost:6301                   ║"
	@echo "  ║  MinIO API         → http://localhost:6302            ║"
	@echo "  ║  MinIO Console     → http://localhost:6303            ║"
	@echo "  ║  RabbitMQ AMQP     → localhost:6304                   ║"
	@echo "  ║  RabbitMQ Mgmt     → http://localhost:6305            ║"
	@echo "  ╠══════════════════════════════════════════════════════╣"
	@echo "  ║  OBSERVABILITY                                        ║"
	@echo "  ║  pgAdmin           → http://localhost:6100            ║"
	@echo "  ║  Prometheus        → http://localhost:6200            ║"
	@echo "  ║  Grafana           → http://localhost:6201            ║"
	@echo "  ╚══════════════════════════════════════════════════════╝"
	@echo ""

## quick-start: full setup from scratch
quick-start:
	@echo "Copying .env.example → .env (if not exists)..."
	@test -f .env || cp .env.example .env
	@echo "Starting all services..."
	$(COMPOSE) --env-file $(ENV_FILE) up -d --build
	@echo ""
	@echo "Waiting for services to be healthy..."
	@sleep 5
	@$(MAKE) urls
