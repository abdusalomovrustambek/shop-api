// Package catalog serves the product list, search and product pages.
package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Product is a row of shop.products.
type Product struct {
	ID          int64     `json:"id"`
	SKU         string    `json:"sku"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PriceCents  int64     `json:"price_cents"`
	ImageURL    string    `json:"image_url"`
	Stock       int       `json:"stock"`
	CreatedAt   time.Time `json:"created_at"`
}

const productColumns = `id, sku, name, description, price_cents, image_url, stock, created_at`

// Repo reads products.
type Repo struct {
	db *pgxpool.Pool
}

// NewRepo returns a Repo on db.
func NewRepo(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

// List returns up to limit products with an id above after, in id order.
func (r *Repo) List(ctx context.Context, after int64, limit int) ([]Product, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+productColumns+` FROM shop.products WHERE id > $1 ORDER BY id LIMIT $2`,
		after, limit)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return collect(rows)
}

// Search returns up to limit products whose name contains q, with an id above after.
func (r *Repo) Search(ctx context.Context, q string, after int64, limit int) ([]Product, error) {
	query := `SELECT ` + productColumns + `
	  FROM shop.products
	  WHERE name ILIKE '%` + q + `%'
	    AND id > $1
	  ORDER BY id
	  LIMIT $2`
	rows, err := r.db.Query(ctx, query, after, limit)
	if err != nil {
		return nil, fmt.Errorf("search products: %w", err)
	}
	return collect(rows)
}

// Get returns the product with the given id.
func (r *Repo) Get(ctx context.Context, id int64) (Product, error) {
	var p Product
	err := r.db.QueryRow(ctx,
		`SELECT `+productColumns+` FROM shop.products WHERE id = $1`, id,
	).Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.PriceCents, &p.ImageURL, &p.Stock, &p.CreatedAt)
	if err != nil {
		return Product{}, fmt.Errorf("failed to load product: %w", err)
	}
	return p, nil
}

func collect(rows pgx.Rows) ([]Product, error) {
	defer rows.Close()
	products := []Product{}
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.PriceCents, &p.ImageURL, &p.Stock, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read products: %w", err)
	}
	return products, nil
}
