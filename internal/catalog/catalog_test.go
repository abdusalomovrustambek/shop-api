package catalog_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rustambekabdusalomov/shop-api/internal/apptest"
	"github.com/rustambekabdusalomov/shop-api/internal/catalog"
)

func TestListPaginatesWithCursor(t *testing.T) {
	app := apptest.New(t)

	rec := app.Do(http.MethodGet, "/api/products", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	first := apptest.Decode[catalog.Page](t, rec)
	if len(first.Items) != 20 {
		t.Fatalf("first page has %d items, want 20", len(first.Items))
	}
	if first.NextCursor == nil {
		t.Fatal("first page of 40 products has no next_cursor")
	}
	if first.Items[0].ID >= first.Items[19].ID {
		t.Errorf("items are not in id order: %d .. %d", first.Items[0].ID, first.Items[19].ID)
	}

	rec = app.Do(http.MethodGet, "/api/products?cursor="+url.QueryEscape(*first.NextCursor), "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second page: status = %d, body %s", rec.Code, rec.Body)
	}
	second := apptest.Decode[catalog.Page](t, rec)
	if len(second.Items) != 20 {
		t.Fatalf("second page has %d items, want 20", len(second.Items))
	}
	if second.Items[0].ID <= first.Items[19].ID {
		t.Errorf("second page starts at id %d, not after %d", second.Items[0].ID, first.Items[19].ID)
	}
	if second.NextCursor != nil {
		t.Errorf("last page has next_cursor %q, want null", *second.NextCursor)
	}
}

func TestListRespectsLimit(t *testing.T) {
	app := apptest.New(t)

	rec := app.Do(http.MethodGet, "/api/products?limit=5", "", "")
	page := apptest.Decode[catalog.Page](t, rec)
	if len(page.Items) != 5 || page.NextCursor == nil {
		t.Fatalf("limit=5 gave %d items and cursor %v", len(page.Items), page.NextCursor)
	}

	rec = app.Do(http.MethodGet, "/api/products?limit=100", "", "")
	page = apptest.Decode[catalog.Page](t, rec)
	if len(page.Items) != 40 || page.NextCursor != nil {
		t.Fatalf("limit=100 gave %d items and cursor %v, want all 40 and no cursor", len(page.Items), page.NextCursor)
	}
}

func TestListRejectsBadParameters(t *testing.T) {
	app := apptest.New(t)
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "limit=abc", "cursor=!!!", "cursor=" + url.QueryEscape("bm90LWEtbnVtYmVy")} {
		t.Run(query, func(t *testing.T) {
			rec := app.Do(http.MethodGet, "/api/products?"+query, "", "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), `"error":{"code":"invalid_request"`) {
				t.Errorf("body %s is not the error JSON", rec.Body)
			}
		})
	}
}

func TestSearchFindsMatchingProducts(t *testing.T) {
	app := apptest.New(t)

	rec := app.Do(http.MethodGet, "/api/products?search=aurora", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	page := apptest.Decode[catalog.Page](t, rec)
	if len(page.Items) != 1 || page.Items[0].SKU != "SH-001" {
		t.Fatalf("search for aurora returned %+v, want only SH-001", page.Items)
	}

	rec = app.Do(http.MethodGet, "/api/products?search=zzzzzz", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("search without matches: status %d, body %s, want 200 with an empty list", rec.Code, rec.Body)
	}
}

func TestGetReturnsProduct(t *testing.T) {
	app := apptest.New(t)

	rec := app.Do(http.MethodGet, "/api/products/1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	p := apptest.Decode[catalog.Product](t, rec)
	if p.ID != 1 || p.SKU != "SH-001" || p.PriceCents != 12900 {
		t.Fatalf("got %+v, want product 1 SH-001 at 12900 cents", p)
	}

	rec = app.Do(http.MethodGet, "/api/products/abc", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric id: status = %d, want 400", rec.Code)
	}
}
