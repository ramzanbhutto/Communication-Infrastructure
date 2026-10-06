.PHONY: deps up dev-api dev-web build test test-backend test-web test-unit check

deps:
	go mod download
	cd web && npm ci

up:
	docker compose up -d --wait postgres redis

dev-api:
	DEMO_MODE=true INFRA_LAB=true go run ./cmd/ops

dev-web:
	cd web && npm run dev

build:
	go build -o bin/ops ./cmd/ops
	cd web && npm run build

test-unit:
	go test ./...

test-backend:
	docker compose --profile test up -d --wait postgres-test redis-test
	TEST_DATABASE_URL='postgres://ops_app:ops_demo_app@127.0.0.1:55443/covent_ops_test?sslmode=disable' TEST_MIGRATION_DATABASE_URL='postgres://ops_admin:ops_demo_admin@127.0.0.1:55443/covent_ops_test?sslmode=disable' TEST_REDIS_URL='redis://127.0.0.1:56390/0' go test -race -count=1 ./...

test-web:
	docker compose --profile test up -d --wait postgres-test redis-test
	cd web && npm run test:e2e

test:
	$(MAKE) test-backend
	$(MAKE) test-web

check:
	go vet ./...
	cd web && npm run typecheck
