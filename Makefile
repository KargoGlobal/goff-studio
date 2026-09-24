BINARY := goff-studio

# Nested modules are invisible to ./... , so every target has to name them.
GO_MODULES := . backends/s3 e2e/harness

.PHONY: build web test lint e2e dev clean

build: web
	go build -o $(BINARY) ./cmd/goff-studio

web:
	cd web && npm install --silent && npm run build
	@touch cmd/goff-studio/dist/.gitkeep

test:
	@for mod in $(GO_MODULES); do \
		echo "==> go test $$mod"; \
		(cd $$mod && go test -race ./...) || exit 1; \
	done
	cd web && npx tsc -b && npx vitest run

e2e:
	cd e2e && npm test

lint:
	@bad=$$(gofmt -l . | grep -v '/node_modules/' || true); \
	if [ -n "$$bad" ]; then echo "gofmt needed:"; echo "$$bad"; exit 1; fi
	@for mod in $(GO_MODULES); do \
		echo "==> go vet $$mod"; \
		(cd $$mod && go vet ./...) || exit 1; \
	done
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "==> golangci-lint"; golangci-lint run ./... || exit 1; \
	else \
		echo "==> golangci-lint not installed, skipping (CI runs it)"; \
	fi
	cd web && npx tsc -b && npm run lint

dev:
	@echo "run these in two terminals:"
	@echo "  go run ./cmd/goff-studio -config studio.yaml"
	@echo "  cd web && npm run dev"

clean:
	rm -rf $(BINARY) cmd/goff-studio/dist/assets
