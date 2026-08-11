.PHONY: build test lint cover cover-check run \
	migrate-up migrate-down migrate-new \
	docker-build docker-run docker-up docker-down clean proto

# Regenerate Go stubs from proto/wallet.proto via buf.
# Requires buf + protoc-gen-go + protoc-gen-go-grpc on PATH (run
# `go install github.com/bufbuild/buf/cmd/buf@latest \
#	google.golang.org/protobuf/cmd/protoc-gen-go@latest \
#	google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`).
proto:
	buf generate

build:
	go build -o bin/wallet-management ./cmd/wallet-management

# coverpkg excludes internal/pb (generated protobuf code) so the coverage
# number reflects hand-written code only. pb is also ignored by Codecov.
COVERPKG = $(shell go list ./internal/... | grep -v '/internal/pb$$' | tr '\n' ',')

test:
	go test ./internal/... -race -count=1 -timeout 120s -coverprofile=coverage.out -coverpkg=$(COVERPKG)

lint:
	golangci-lint run

cover: test
	go tool cover -func=coverage.out | tail -1

# cover-check runs the test suite and fails if total coverage (excluding
# internal/pb) falls below 80%.
cover-check:
	go test ./internal/... -race -count=1 -timeout 120s -coverprofile=coverage.out -coverpkg=$(COVERPKG)
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,"",$$NF); print $$NF}'); \
	echo "Coverage: $$total%"; \
	awk -v t=$$total 'BEGIN { exit !(t >= 80) }'; \
	if [ $$? -ne 0 ]; then \
		echo "ERROR: coverage $$total% is below 80% threshold"; \
		exit 1; \
	fi

run:
	go run ./cmd/wallet-management

migrate-up:
	go run ./cmd/migrate --up

migrate-down:
	go run ./cmd/migrate --down

migrate-new:
	@test -n "$(NAME)" || (echo "usage: make migrate-new NAME=add_widgets" && exit 1)
	@next=$$(printf '%04d' $$(( $$(ls migrations/*.up.sql 2>/dev/null | wc -l | tr -d ' ') + 1 ))); \
	touch migrations/$${next}_$(NAME).up.sql migrations/$${next}_$(NAME).down.sql; \
	echo "created migrations/$${next}_$(NAME).{up,down}.sql"

docker-build:
	docker build -t ai-crypto-onramp/wallet-manager .

docker-run:
	docker run --rm -p 8080:8080 -p 9090:9090 ai-crypto-onramp/wallet-manager

docker-up:
	docker compose up -d --wait

docker-down:
	docker compose down

clean:
	rm -rf bin/ coverage.out
