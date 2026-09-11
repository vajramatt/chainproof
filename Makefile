.PHONY: build test check
build:
	go build -trimpath -o chainproof ./cmd/chainproof
test:
	go test ./...
	cd integrations/openclaw && npm ci && npm run typecheck
check:
	gofmt -w cmd internal
	go vet ./...
	go test ./...
	sh scripts/install_test.sh
	./scripts/check-version.sh
