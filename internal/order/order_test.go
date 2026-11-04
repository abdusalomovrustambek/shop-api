package order_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustambekabdusalomov/shop-api/internal/apptest"
	"github.com/rustambekabdusalomov/shop-api/internal/auth"
	"github.com/rustambekabdusalomov/shop-api/internal/order"
	"github.com/rustambekabdusalomov/shop-api/internal/server"
	"github.com/rustambekabdusalomov/shop-api/internal/testdb"
)

const (
	alice = "aaaaaaaa-0000-0000-0000-000000000001"
	bob   = "bbbbbbbb-0000-0000-0000-000000000002"

	cardNumber  = "4111111111111111"
	checkoutUSD = `{"currency":"USD","card":{"number":"4111111111111111","exp":"12/29","cvc":"123"}}`
)

func addToCart(t *testing.T, app *apptest.App, token string, productID int64, quantity int) {
	t.Helper()
	body := fmt.Sprintf(`{"product_id":%d,"quantity":%d}`, productID, quantity)
	if rec := app.Do(http.MethodPost, "/api/cart/items", token, body); rec.Code != http.StatusCreated {
		t.Fatalf("add to cart: status %d, body %s", rec.Code, rec.Body)
	}
}

func count(t *testing.T, app *apptest.App, query string, args ...any) int {
	t.Helper()
	var n int
	if err := app.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func stockOf(t *testing.T, app *apptest.App, productID int64) int {
	t.Helper()
	return count(t, app, `SELECT stock FROM shop.products WHERE id = $1`, productID)
}

func TestCheckoutCreatesOrderAndJob(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 2000, 10)
	addToCart(t, app, token, id, 2)
	if rec := app.Do(http.MethodPost, "/api/cart/coupon", token, `{"code":"WELCOME10"}`); rec.Code != http.StatusOK {
		t.Fatalf("coupon: status %d, body %s", rec.Code, rec.Body)
	}

	rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout: status %d, body %s", rec.Code, rec.Body)
	}
	o := apptest.Decode[order.Order](t, rec)
	if o.SubtotalCents != 4000 || o.DiscountCents != 400 || o.TotalCents != 3600 {
		t.Fatalf("amounts = %d / %d / %d, want 4000 / 400 / 3600", o.SubtotalCents, o.DiscountCents, o.TotalCents)
	}
	if o.TotalCents != o.SubtotalCents-o.DiscountCents {
		t.Errorf("total %d != subtotal %d - discount %d", o.TotalCents, o.SubtotalCents, o.DiscountCents)
	}
	if o.Status != "pending" || o.Currency != "USD" || o.CouponCode == nil || *o.CouponCode != "WELCOME10" {
		t.Errorf("order = %+v", o)
	}
	if len(o.Items) != 1 || o.Items[0].Quantity != 2 || o.Items[0].UnitPriceCents != 2000 || o.Items[0].Name != "Test T-1" {
		t.Errorf("items = %+v", o.Items)
	}

	if n := count(t, app, `SELECT count(*) FROM shop.orders`); n != 1 {
		t.Errorf("%d orders, want 1", n)
	}
	if n := count(t, app, `SELECT count(*) FROM shop.order_items WHERE order_id = $1`, o.ID); n != 1 {
		t.Errorf("%d order items, want 1", n)
	}
	if got := stockOf(t, app, id); got != 8 {
		t.Errorf("stock = %d, want 8", got)
	}
	if n := count(t, app, `SELECT count(*) FROM shop.cart_items`); n != 0 {
		t.Errorf("%d cart items left, want 0", n)
	}
	if n := count(t, app, `SELECT count(*) FROM shop.carts WHERE coupon_code IS NOT NULL`); n != 0 {
		t.Errorf("the cart still holds its coupon")
	}

	if n := count(t, app, `SELECT count(*) FROM billing.jobs`); n != 1 {
		t.Fatalf("%d billing jobs, want exactly 1", n)
	}
	var kind, orderID, totalCents, currency, userID, status, payloadText string
	var hasCard bool
	err := app.Pool.QueryRow(context.Background(),
		`SELECT kind, payload->>'order_id', payload->>'total_cents', payload->>'currency',
		        payload->>'user_id', status, payload ? 'card', payload::text
		   FROM billing.jobs`,
	).Scan(&kind, &orderID, &totalCents, &currency, &userID, &status, &hasCard, &payloadText)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if kind != "charge_order" || orderID != fmt.Sprint(o.ID) || totalCents != "3600" || currency != "USD" || userID != alice || status != "queued" {
		t.Errorf("job = %s %s %s %s %s %s", kind, orderID, totalCents, currency, userID, status)
	}
	// The job must never carry card data: no card object, and neither the PAN
	// nor the CVC anywhere in the stored JSON (CWE-312).
	if hasCard {
		t.Errorf("billing job payload contains a card object: %s", payloadText)
	}
	if strings.Contains(payloadText, cardNumber) {
		t.Errorf("billing job payload contains the card number: %s", payloadText)
	}
	if strings.Contains(payloadText, `"cvc"`) {
		t.Errorf("billing job payload contains the CVC: %s", payloadText)
	}

	rec = app.Do(http.MethodGet, "/api/orders", token, "")
	list := apptest.Decode[[]order.Order](t, rec)
	if rec.Code != http.StatusOK || len(list) != 1 || list[0].ID != o.ID || list[0].TotalCents != 3600 {
		t.Fatalf("GET /api/orders: status %d, %+v", rec.Code, list)
	}
}

// TestBillingJobPayloadHasNoCardData is the SEC-4 regression test: a checkout
// must not persist the buyer's PAN, expiry or CVC in billing.jobs.
func TestBillingJobPayloadHasNoCardData(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 1)
	addToCart(t, app, token, id, 1)

	if rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD); rec.Code != http.StatusCreated {
		t.Fatalf("checkout: status %d, body %s", rec.Code, rec.Body)
	}

	var hasCard bool
	var payloadText string
	err := app.Pool.QueryRow(context.Background(),
		`SELECT payload ? 'card', payload::text FROM billing.jobs`,
	).Scan(&hasCard, &payloadText)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if hasCard {
		t.Errorf("billing job payload contains a card object: %s", payloadText)
	}
	if strings.Contains(payloadText, cardNumber) {
		t.Errorf("billing job payload contains the card number: %s", payloadText)
	}
	if strings.Contains(payloadText, `"cvc"`) {
		t.Errorf("billing job payload contains the CVC: %s", payloadText)
	}
}

func TestCardNeverReachesTheLogs(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 10)
	addToCart(t, app, token, id, 1)

	if rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD); rec.Code != http.StatusCreated {
		t.Fatalf("checkout: status %d, body %s", rec.Code, rec.Body)
	}

	// A checkout that fails on the server logs its inputs; the card must not be one of them.
	if _, err := app.Pool.Exec(context.Background(), `DROP TABLE billing.jobs`); err != nil {
		t.Fatalf("drop billing.jobs: %v", err)
	}
	addToCart(t, app, token, id, 1)
	if rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD); rec.Code != http.StatusInternalServerError {
		t.Fatalf("checkout without billing.jobs: status %d, want 500", rec.Code)
	}
	if !strings.Contains(app.Logs.String(), "checkout failed") {
		t.Fatalf("the failed checkout was not logged: %s", app.Logs.String())
	}
	for name, out := range map[string]string{"logs": app.Logs.String(), "stderr": app.Stderr.String()} {
		if strings.Contains(out, cardNumber) {
			t.Errorf("card number found in the %s", name)
		}
	}
}

func TestCheckoutEmptyCart(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)

	if rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD); rec.Code != http.StatusBadRequest {
		t.Fatalf("no cart: status %d, want 400; body %s", rec.Code, rec.Body)
	}
	if rec := app.Do(http.MethodGet, "/api/cart", token, ""); rec.Code != http.StatusOK {
		t.Fatalf("create cart: status %d", rec.Code)
	}
	if rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty cart: status %d, want 400; body %s", rec.Code, rec.Body)
	}
	if n := count(t, app, `SELECT count(*) FROM shop.orders`); n != 0 {
		t.Errorf("%d orders, want 0", n)
	}
}

func TestCheckoutWithShortStockWritesNothing(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	plenty := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 10)
	scarce := testdb.InsertProduct(t, app.Pool, "T-2", 1000, 1)
	addToCart(t, app, token, plenty, 2)
	addToCart(t, app, token, scarce, 2)

	rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body)
	}
	if got := stockOf(t, app, plenty); got != 10 {
		t.Errorf("stock of the first product = %d, want 10", got)
	}
	if got := stockOf(t, app, scarce); got != 1 {
		t.Errorf("stock of the second product = %d, want 1", got)
	}
	for _, table := range []string{"shop.orders", "shop.order_items", "billing.jobs"} {
		if n := count(t, app, `SELECT count(*) FROM `+table); n != 0 {
			t.Errorf("%s has %d rows, want 0", table, n)
		}
	}
	if n := count(t, app, `SELECT count(*) FROM shop.cart_items`); n != 2 {
		t.Errorf("cart has %d items, want it untouched with 2", n)
	}
}

func TestConcurrentCheckoutsForTheLastUnit(t *testing.T) {
	app := apptest.New(t)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 1)
	tokens := []string{app.Token(t, alice), app.Token(t, bob)}
	for _, token := range tokens {
		addToCart(t, app, token, id, 1)
	}

	codes := make([]int, len(tokens))
	var wg sync.WaitGroup
	for i, token := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD).Code
		}()
	}
	wg.Wait()

	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("status codes %v, want one 201 and one 409", codes)
	}
	if got := stockOf(t, app, id); got != 0 {
		t.Errorf("stock = %d, want 0", got)
	}
	if n := count(t, app, `SELECT count(*) FROM billing.jobs`); n != 1 {
		t.Errorf("%d billing jobs, want 1", n)
	}
}

func TestCheckoutCoupons(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1200, 10)
	addToCart(t, app, token, id, 1)

	for _, code := range []string{"EXPIRED5", "NOSUCHCODE", "FREESHIP"} {
		body := fmt.Sprintf(`{"currency":"USD","coupon_code":%q,"card":{"number":"4111111111111111","exp":"12/29","cvc":"123"}}`, code)
		if rec := app.Do(http.MethodPost, "/api/checkout", token, body); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d, want 422; body %s", code, rec.Code, rec.Body)
		}
	}
	if n := count(t, app, `SELECT count(*) FROM shop.orders`); n != 0 {
		t.Fatalf("a refused coupon still created %d orders", n)
	}
	if got := stockOf(t, app, id); got != 10 {
		t.Fatalf("a refused coupon changed the stock to %d", got)
	}

	// A fixed discount above the subtotal is capped: the total is never negative.
	body := `{"currency":"USD","coupon_code":"TEAM50","card":{"number":"4111111111111111","exp":"12/29","cvc":"123"}}`
	rec := app.Do(http.MethodPost, "/api/checkout", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("TEAM50: status %d, body %s", rec.Code, rec.Body)
	}
	o := apptest.Decode[order.Order](t, rec)
	if o.SubtotalCents != 1200 || o.DiscountCents != 1200 || o.TotalCents != 0 {
		t.Errorf("amounts = %d / %d / %d, want 1200 / 1200 / 0", o.SubtotalCents, o.DiscountCents, o.TotalCents)
	}
}

func TestCheckoutValidatesTheRequest(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 50)
	addToCart(t, app, token, id, 1)

	card := `"card":{"number":"4111111111111111","exp":"12/29","cvc":"123"}`
	tests := []struct {
		name string
		body string
		want int
	}{
		{"lowercase currency", `{"currency":"usd",` + card + `}`, http.StatusBadRequest},
		{"two letter currency", `{"currency":"US",` + card + `}`, http.StatusBadRequest},
		{"four letter currency", `{"currency":"USDX",` + card + `}`, http.StatusBadRequest},
		{"digit in currency", `{"currency":"U5D",` + card + `}`, http.StatusBadRequest},
		{"no currency", `{` + card + `}`, http.StatusBadRequest},
		{"no card", `{"currency":"USD"}`, http.StatusBadRequest},
		{"short card number", `{"currency":"USD","card":{"number":"4111","exp":"12/29","cvc":"123"}}`, http.StatusBadRequest},
		{"card number with letters", `{"currency":"USD","card":{"number":"41111111111111xx","exp":"12/29","cvc":"123"}}`, http.StatusBadRequest},
		{"bad cvc", `{"currency":"USD","card":{"number":"4111111111111111","exp":"12/29","cvc":"1"}}`, http.StatusBadRequest},
		{"unknown field", `{"currency":"USD","total":1,` + card + `}`, http.StatusBadRequest},
		{"malformed JSON", `{"currency":`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
		{"UZS is accepted", `{"currency":"UZS",` + card + `}`, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := app.Do(http.MethodPost, "/api/checkout", token, tt.body)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.want, rec.Body)
			}
		})
	}
	if n := count(t, app, `SELECT count(*) FROM shop.orders`); n != 1 {
		t.Errorf("%d orders, want only the UZS one", n)
	}
}

func TestCheckoutNeedsToken(t *testing.T) {
	app := apptest.New(t)
	if rec := app.Do(http.MethodPost, "/api/checkout", "", checkoutUSD); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestOrderOfAnotherUserIsNotFound(t *testing.T) {
	app := apptest.New(t)
	aliceToken, bobToken := app.Token(t, alice), app.Token(t, bob)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 10)
	addToCart(t, app, aliceToken, id, 1)
	rec := app.Do(http.MethodPost, "/api/checkout", aliceToken, checkoutUSD)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout: status %d, body %s", rec.Code, rec.Body)
	}
	path := fmt.Sprintf("/api/orders/%d", apptest.Decode[order.Order](t, rec).ID)

	if rec := app.Do(http.MethodGet, path, aliceToken, ""); rec.Code != http.StatusOK {
		t.Errorf("own order: status %d, want 200", rec.Code)
	}
	if rec := app.Do(http.MethodGet, path, bobToken, ""); rec.Code != http.StatusNotFound {
		t.Errorf("someone else's order: status %d, want 404", rec.Code)
	}
	if rec := app.Do(http.MethodGet, path, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("order without a token: status %d, want 401", rec.Code)
	}
	if rec := app.Do(http.MethodGet, "/api/orders/999999", aliceToken, ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing order: status %d, want 404", rec.Code)
	}
	if rec := app.Do(http.MethodGet, "/api/orders/abc", aliceToken, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad order id: status %d, want 400", rec.Code)
	}
	if list := apptest.Decode[[]order.Order](t, app.Do(http.MethodGet, "/api/orders", bobToken, "")); len(list) != 0 {
		t.Errorf("bob sees %d orders, want 0", len(list))
	}
}

func TestOrdersListNewestFirst(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 50)

	var ids []int64
	for i := 0; i < 3; i++ {
		addToCart(t, app, token, id, 1)
		rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD)
		if rec.Code != http.StatusCreated {
			t.Fatalf("checkout %d: status %d, body %s", i, rec.Code, rec.Body)
		}
		ids = append(ids, apptest.Decode[order.Order](t, rec).ID)
	}

	list := apptest.Decode[[]order.Order](t, app.Do(http.MethodGet, "/api/orders", token, ""))
	if len(list) != 3 {
		t.Fatalf("%d orders, want 3", len(list))
	}
	for i, o := range list {
		if want := ids[len(ids)-1-i]; o.ID != want {
			t.Errorf("position %d is order %d, want %d (newest first)", i, o.ID, want)
		}
		if len(o.Items) != 1 {
			t.Errorf("order %d has %d items, want 1", o.ID, len(o.Items))
		}
	}
}

func TestStoredCouponThatStoppedApplyingIsDropped(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 50)
	path := fmt.Sprintf("/api/cart/items/%d", id)

	// FREESHIP needs a cart of at least 3000 cents; shrinking the cart afterwards must not trap it.
	addToCart(t, app, token, id, 3)
	if rec := app.Do(http.MethodPost, "/api/cart/coupon", token, `{"code":"FREESHIP"}`); rec.Code != http.StatusOK {
		t.Fatalf("coupon: status %d, body %s", rec.Code, rec.Body)
	}
	if rec := app.Do(http.MethodPatch, path, token, `{"quantity":1}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: status %d, body %s", rec.Code, rec.Body)
	}
	rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout with a cart coupon below its minimum: status %d, body %s", rec.Code, rec.Body)
	}
	o := apptest.Decode[order.Order](t, rec)
	if o.SubtotalCents != 1000 || o.DiscountCents != 0 || o.TotalCents != 1000 || o.CouponCode != nil {
		t.Errorf("order = %+v, want full price and no coupon", o)
	}

	// A coupon that expired after it was applied behaves the same.
	addToCart(t, app, token, id, 1)
	if rec := app.Do(http.MethodPost, "/api/cart/coupon", token, `{"code":"WELCOME10"}`); rec.Code != http.StatusOK {
		t.Fatalf("coupon: status %d, body %s", rec.Code, rec.Body)
	}
	if _, err := app.Pool.Exec(context.Background(), `UPDATE shop.coupons SET expires_at = now() - interval '1 hour' WHERE code = 'WELCOME10'`); err != nil {
		t.Fatalf("expire coupon: %v", err)
	}
	rec = app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout with an expired cart coupon: status %d, body %s", rec.Code, rec.Body)
	}
	if o := apptest.Decode[order.Order](t, rec); o.DiscountCents != 0 || o.CouponCode != nil {
		t.Errorf("order = %+v, want full price and no coupon", o)
	}

	// The same coupon named in the request is refused.
	addToCart(t, app, token, id, 1)
	body := `{"currency":"USD","coupon_code":"WELCOME10","card":{"number":"4111111111111111","exp":"12/29","cvc":"123"}}`
	if rec := app.Do(http.MethodPost, "/api/checkout", token, body); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expired coupon in the request: status %d, want 422", rec.Code)
	}
}

func TestRequestedCouponWinsOverTheCartCoupon(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 2000, 50)
	addToCart(t, app, token, id, 1)
	if rec := app.Do(http.MethodPost, "/api/cart/coupon", token, `{"code":"WELCOME10"}`); rec.Code != http.StatusOK {
		t.Fatalf("coupon: status %d, body %s", rec.Code, rec.Body)
	}

	body := `{"currency":"USD","coupon_code":"TEAM50","card":{"number":"4111111111111111","exp":"12/29","cvc":"123"}}`
	rec := app.Do(http.MethodPost, "/api/checkout", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout: status %d, body %s", rec.Code, rec.Body)
	}
	o := apptest.Decode[order.Order](t, rec)
	// TEAM50 is a fixed 5000 capped at the subtotal; WELCOME10 would have given 200.
	if o.DiscountCents != 2000 || o.TotalCents != 0 || o.CouponCode == nil || *o.CouponCode != "TEAM50" {
		t.Errorf("order = %+v, want TEAM50 with discount 2000", o)
	}
}

func TestCheckoutRollsBackWhenTheJobCannotBeQueued(t *testing.T) {
	app := apptest.New(t)
	token := app.Token(t, alice)
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 10)
	addToCart(t, app, token, id, 2)
	if _, err := app.Pool.Exec(context.Background(), `DROP TABLE billing.jobs`); err != nil {
		t.Fatalf("drop billing.jobs: %v", err)
	}

	// The order, its items and the stock decrement are written before the job insert fails.
	if rec := app.Do(http.MethodPost, "/api/checkout", token, checkoutUSD); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", rec.Code, rec.Body)
	}
	for _, table := range []string{"shop.orders", "shop.order_items"} {
		if n := count(t, app, `SELECT count(*) FROM `+table); n != 0 {
			t.Errorf("%s has %d rows after the failed checkout, want 0", table, n)
		}
	}
	if got := stockOf(t, app, id); got != 10 {
		t.Errorf("stock = %d, want 10", got)
	}
	if n := count(t, app, `SELECT coalesce(sum(quantity), 0) FROM shop.cart_items`); n != 2 {
		t.Errorf("cart holds %d units, want it untouched with 2", n)
	}
}

func TestConcurrentCouponCheckoutsDoNotExhaustTheConnectionPool(t *testing.T) {
	app := apptest.New(t)
	tokens := []string{app.Token(t, alice), app.Token(t, bob)}
	id := testdb.InsertProduct(t, app.Pool, "T-1", 1000, 10)
	for _, token := range tokens {
		addToCart(t, app, token, id, 1)
		if rec := app.Do(http.MethodPost, "/api/cart/coupon", token, `{"code":"WELCOME10"}`); rec.Code != http.StatusOK {
			t.Fatalf("coupon: status %d, body %s", rec.Code, rec.Body)
		}
	}

	// As many connections as concurrent checkouts: a checkout that needs a second
	// connection while it holds one would wait for the other forever.
	cfg := app.Pool.Config()
	cfg.MaxConns = 2
	small, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("small pool: %v", err)
	}
	t.Cleanup(small.Close)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := server.New(server.Deps{Pool: small, Verifier: auth.NewVerifier(app.Issuer.URL(), log), Log: log, Stderr: io.Discard})

	codes := make(chan int, len(tokens))
	for _, token := range tokens {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/checkout", strings.NewReader(checkoutUSD))
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			codes <- rec.Code
		}()
	}
	for range tokens {
		select {
		case code := <-codes:
			if code != http.StatusCreated {
				t.Errorf("checkout: status %d, want 201", code)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("checkouts are stuck: they wait for pool connections they cannot get")
		}
	}
}
