package cart_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rustambekabdusalomov/shop-api/internal/apptest"
	"github.com/rustambekabdusalomov/shop-api/internal/cart"
	"github.com/rustambekabdusalomov/shop-api/internal/testdb"
)

const user = "aaaaaaaa-0000-0000-0000-000000000001"

func addBody(productID int64, quantity int) string {
	return fmt.Sprintf(`{"product_id":%d,"quantity":%d}`, productID, quantity)
}

func patchBody(quantity int) string {
	return fmt.Sprintf(`{"quantity":%d}`, quantity)
}

func getCart(t *testing.T, app *apptest.App, token string) cart.View {
	t.Helper()
	rec := app.Do(http.MethodGet, "/api/cart", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/cart: status %d, body %s", rec.Code, rec.Body)
	}
	return apptest.Decode[cart.View](t, rec)
}

func TestCartNeedsToken(t *testing.T) {
	app := apptest.New(t)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/cart", ""},
		{http.MethodPost, "/api/cart/items", addBody(1, 1)},
		{http.MethodPatch, "/api/cart/items/1", patchBody(1)},
		{http.MethodPost, "/api/cart/coupon", `{"code":"WELCOME10"}`},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := app.Do(tc.method, tc.path, "", tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestCartIsCreatedOnDemand(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, user)

	rec := app.Do(http.MethodGet, "/api/cart", token, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("status %d, body %s, want 200 with an empty item list", rec.Code, rec.Body)
	}
	v := apptest.Decode[cart.View](t, rec)
	if v.SubtotalCents != 0 || v.DiscountCents != 0 || v.TotalCents != 0 || v.CouponCode != nil {
		t.Errorf("empty cart = %+v, want zero totals and no coupon", v)
	}
}

func TestAddItemQuantityBounds(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, user)
	one := testdb.InsertProduct(t, app.Pool, "T-1", 2000, 500)
	many := testdb.InsertProduct(t, app.Pool, "T-2", 100, 500)

	tests := []struct {
		name      string
		productID int64
		quantity  int
		want      int
	}{
		{"quantity 1", one, 1, http.StatusCreated},
		{"quantity 99", many, 99, http.StatusCreated},
		{"quantity 100", one, 100, http.StatusBadRequest},
		{"quantity -1", one, -1, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := app.Do(http.MethodPost, "/api/cart/items", token, addBody(tt.productID, tt.quantity))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.want, rec.Body)
			}
		})
	}

	v := getCart(t, app, token)
	if len(v.Items) != 2 {
		t.Fatalf("cart has %d lines, want 2: %+v", len(v.Items), v.Items)
	}
	if want := int64(1*2000 + 99*100); v.SubtotalCents != want || v.TotalCents != want {
		t.Errorf("subtotal %d total %d, want %d", v.SubtotalCents, v.TotalCents, want)
	}
}

func TestAddItemAddsUpToTheMaximum(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, user)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 100, 500)

	for i := 0; i < 2; i++ {
		rec := app.Do(http.MethodPost, "/api/cart/items", token, addBody(id, 60))
		if rec.Code != http.StatusCreated {
			t.Fatalf("add %d: status %d, body %s", i, rec.Code, rec.Body)
		}
	}
	v := getCart(t, app, token)
	if len(v.Items) != 1 || v.Items[0].Quantity != cart.MaxQuantity {
		t.Fatalf("cart = %+v, want one line capped at %d", v.Items, cart.MaxQuantity)
	}
}

func TestAddItemRejectsBadInput(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, user)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 100, 5)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"unknown product", addBody(id+1000, 1), http.StatusNotFound},
		{"missing product id", `{"quantity":1}`, http.StatusBadRequest},
		{"malformed JSON", `{"product_id":`, http.StatusBadRequest},
		{"unknown field", fmt.Sprintf(`{"product_id":%d,"quantity":1,"price":1}`, id), http.StatusBadRequest},
		{"wrong type", `{"product_id":"x","quantity":1}`, http.StatusBadRequest},
		{"two documents", addBody(id, 1) + addBody(id, 1), http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := app.Do(http.MethodPost, "/api/cart/items", token, tt.body)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.want, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), `"error":{"code":`) {
				t.Errorf("body %s is not the error JSON", rec.Body)
			}
		})
	}
}

func TestPatchQuantity(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, user)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 100, 500)
	other := testdb.InsertProduct(t, app.Pool, "T-2", 100, 500)
	if rec := app.Do(http.MethodPost, "/api/cart/items", token, addBody(id, 1)); rec.Code != http.StatusCreated {
		t.Fatalf("setup: status %d, body %s", rec.Code, rec.Body)
	}
	path := fmt.Sprintf("/api/cart/items/%d", id)

	rec := app.Do(http.MethodPatch, path, token, patchBody(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("set 7: status %d, body %s", rec.Code, rec.Body)
	}
	if v := apptest.Decode[cart.View](t, rec); len(v.Items) != 1 || v.Items[0].Quantity != 7 {
		t.Fatalf("after set 7: %+v", v.Items)
	}

	for _, tc := range []struct {
		name, path, body string
		want             int
	}{
		{"above the maximum", path, patchBody(100), http.StatusBadRequest},
		{"negative", path, patchBody(-1), http.StatusBadRequest},
		{"no quantity", path, `{}`, http.StatusBadRequest},
		{"product not in the cart", fmt.Sprintf("/api/cart/items/%d", other), patchBody(2), http.StatusNotFound},
		{"removing a product not in the cart", fmt.Sprintf("/api/cart/items/%d", other), patchBody(0), http.StatusNotFound},
		{"bad product id", "/api/cart/items/abc", patchBody(1), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := app.Do(http.MethodPatch, tc.path, token, tc.body); rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body)
			}
		})
	}

	rec = app.Do(http.MethodPatch, path, token, patchBody(0))
	if rec.Code != http.StatusOK {
		t.Fatalf("set 0: status %d, body %s", rec.Code, rec.Body)
	}
	if v := apptest.Decode[cart.View](t, rec); len(v.Items) != 0 || v.TotalCents != 0 {
		t.Fatalf("after set 0 the item must be gone: %+v", v)
	}
}

func TestApplyCoupon(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, user)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 2000, 500)
	if rec := app.Do(http.MethodPost, "/api/cart/items", token, addBody(id, 1)); rec.Code != http.StatusCreated {
		t.Fatalf("setup: status %d, body %s", rec.Code, rec.Body)
	}

	rec := app.Do(http.MethodPost, "/api/cart/coupon", token, `{"code":"welcome10"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("WELCOME10: status %d, body %s", rec.Code, rec.Body)
	}
	v := apptest.Decode[cart.View](t, rec)
	if v.CouponCode == nil || *v.CouponCode != "WELCOME10" || v.SubtotalCents != 2000 || v.DiscountCents != 200 || v.TotalCents != 1800 {
		t.Fatalf("after WELCOME10: %+v, want subtotal 2000, discount 200, total 1800", v)
	}
	if got := getCart(t, app, token); got.DiscountCents != 200 || got.TotalCents != 1800 {
		t.Errorf("GET /api/cart after WELCOME10: %+v", got)
	}

	for _, code := range []string{"EXPIRED5", "NOSUCHCODE", "FREESHIP"} {
		t.Run(code, func(t *testing.T) {
			rec := app.Do(http.MethodPost, "/api/cart/coupon", token, fmt.Sprintf(`{"code":%q}`, code))
			if rec.Code != http.StatusUnprocessable {
				t.Fatalf("status = %d, want 422; body %s", rec.Code, rec.Body)
			}
			if got := getCart(t, app, token); got.TotalCents != 1800 || got.CouponCode == nil || *got.CouponCode != "WELCOME10" {
				t.Errorf("a refused coupon changed the cart: %+v", got)
			}
		})
	}

	if rec := app.Do(http.MethodPost, "/api/cart/coupon", token, `{"code":"  "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("blank code: status %d, want 400", rec.Code)
	}
}

func TestFixedCouponNeverMakesTheTotalNegative(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, user)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1200, 500)
	if rec := app.Do(http.MethodPost, "/api/cart/items", token, addBody(id, 1)); rec.Code != http.StatusCreated {
		t.Fatalf("setup: status %d, body %s", rec.Code, rec.Body)
	}

	rec := app.Do(http.MethodPost, "/api/cart/coupon", token, `{"code":"TEAM50"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("TEAM50: status %d, body %s", rec.Code, rec.Body)
	}
	v := apptest.Decode[cart.View](t, rec)
	if v.DiscountCents != 1200 || v.TotalCents != 0 {
		t.Fatalf("TEAM50 on 1200 cents: %+v, want discount 1200 and total 0", v)
	}
}
