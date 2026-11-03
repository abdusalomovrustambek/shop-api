.PHONY: build lint test run demo-break-ci demo-reset-ci demo-clean

DEMO_PATCH := demo/patches/ci-rounding.patch
DEMO_SUBJECT := refactor(coupon): simplify percent discount

build:
	go build -o bin/shop-api ./cmd/shop-api
	go build -o bin/shopper ./cmd/shopper

lint:
	golangci-lint run

test:
	go test -race ./...

run:
	go run ./cmd/shop-api

# Applies the rounding patch and commits it; push the commit to trigger the red CI run.
demo-break-ci:
	git apply $(DEMO_PATCH)
	git add internal/coupon/coupon.go
	git commit -m "$(DEMO_SUBJECT)"

# Reverts the commit made by demo-break-ci.
demo-reset-ci:
	git revert --no-edit "$$(git log -1 --format=%H -F --grep='$(DEMO_SUBJECT)')"

# Deletes orders older than 7 days (order_items go with them). Needs DATABASE_URL.
demo-clean:
	psql "$$DATABASE_URL" -c "DELETE FROM shop.orders WHERE created_at < now() - interval '7 days'"
