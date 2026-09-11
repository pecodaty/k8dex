KUBERNETES_VERSION ?= v1.34.0

.PHONY: test update-apis verify-generated

test:
	go test ./...

update-apis:
	go run ./cmd/update-apis --kubernetes-version "$(KUBERNETES_VERSION)"
	gofmt -w ./internal/generated

verify-generated:
	@set -eu; \
	verify_dir=$$(mktemp -d); \
	trap 'rm -rf "$$verify_dir"' EXIT; \
	mkdir -p "$$verify_dir/internal"; \
	cp -R internal/generated "$$verify_dir/internal/generated"; \
	cp -R api-changes "$$verify_dir/api-changes"; \
	for catalog in internal/generated/v*_*.json; do \
		version=$$(basename "$$catalog" .json | tr _ .); \
		go run ./cmd/update-apis --kubernetes-version "$$version.0" --output-dir "$$verify_dir/internal/generated" --changes-dir "$$verify_dir/api-changes"; \
	done; \
	diff -ru internal/generated "$$verify_dir/internal/generated"; \
	diff -ru api-changes "$$verify_dir/api-changes"
