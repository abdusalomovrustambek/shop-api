.PHONY: build lint test run demo-clean

build:
	go build -o bin/shop-api ./cmd/shop-api
	go build -o bin/shopper ./cmd/shopper

lint:
	golangci-lint run

test:
	go test -race ./...

run:
	go run ./cmd/shop-api

# Deletes orders older than 7 days (order_items go with them) and restocks products the shopper has bought down.
# Needs DATABASE_URL. Run it daily rather than weekly if the shopper runs all the time.
demo-clean:
	psql "$$DATABASE_URL" -c "DELETE FROM shop.orders WHERE created_at < now() - interval '7 days'" -c "UPDATE shop.products SET stock = GREATEST(stock, 3000)"
