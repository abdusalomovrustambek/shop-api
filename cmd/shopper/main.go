// Command shopper is the traffic generator of the Shop demo. It drives the stack through
// auth-gateway with a fixed pool of accounts: steady shopping, and now and then an incident.
package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	defaultBaseURL = "http://auth-gateway:8080"
	accountCount   = 50
	// bruteAttempts is one more than auth-gateway's sign-in limit from a single address.
	bruteAttempts = 26
	// missingProductID is a product that does not exist.
	missingProductID = 999999
)

var allIncidents = []string{"api-1", "api-2", "api-3", "zero-qty", "uzs", "brute", "scan"}

var (
	searchWords     = []string{"headphones", "coffee", "lamp", "cable", "mug", "keyboard", "bottle", "notebook"}
	injectionProbes = []string{"' UNION SELECT email FROM auth.users --", "' OR sleep(5) --"}
	validCoupons    = []string{"WELCOME10", "TEAM50"}
	scanPaths       = []string{"/admin", "/wp-login.php", "/phpmyadmin/", "/.git/config", "/server-status", "/api/.env", "/backend/.env", "/.env"}
)

type config struct {
	baseURL       string
	incidentEvery time.Duration
	incidents     []string
	dryRun        bool
}

// loadConfig reads the flags and SHOPPER_* variables. SHOPPER_INCIDENTS unset means every
// incident; set but empty means none.
func loadConfig(args []string, lookup func(string) (string, bool)) (config, error) {
	fs := flag.NewFlagSet("shopper", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the requests instead of sending them")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	cfg := config{
		baseURL:       defaultBaseURL,
		incidentEvery: time.Minute,
		incidents:     allIncidents,
		dryRun:        *dryRun,
	}
	if v, ok := lookup("SHOPPER_BASE_URL"); ok && v != "" {
		cfg.baseURL = strings.TrimRight(v, "/")
	}
	if v, ok := lookup("SHOPPER_INCIDENT_EVERY"); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return config{}, fmt.Errorf("SHOPPER_INCIDENT_EVERY must be a positive duration like 60s, got %q", v)
		}
		cfg.incidentEvery = d
	}
	if v, ok := lookup("SHOPPER_INCIDENTS"); ok {
		list, err := parseIncidents(v)
		if err != nil {
			return config{}, err
		}
		cfg.incidents = list
	}
	return cfg, nil
}

// parseIncidents turns "api-1,brute" into a list; an empty value is an empty list.
func parseIncidents(value string) ([]string, error) {
	var list []string
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !slices.Contains(allIncidents, name) {
			return nil, fmt.Errorf("unknown incident %q in SHOPPER_INCIDENTS (known: %s)", name, strings.Join(allIncidents, ", "))
		}
		list = append(list, name)
	}
	return list, nil
}

type account struct {
	email    string
	password string
	ip       string
	token    string
}

type reply struct {
	status int
	body   []byte
}

type shopper struct {
	cfg      config
	client   *http.Client
	rng      *rand.Rand
	log      *slog.Logger
	accounts []*account
	products []int64
	// fast skips the pacing between requests (tests).
	fast bool
}

func newShopper(cfg config, out io.Writer, log *slog.Logger, seed uint64) *shopper {
	client := &http.Client{Timeout: 15 * time.Second}
	if cfg.dryRun {
		client = &http.Client{Transport: dryTransport{out: out}}
	}
	s := &shopper{cfg: cfg, client: client, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), log: log}
	for i := 1; i <= accountCount; i++ {
		s.accounts = append(s.accounts, &account{
			email:    fmt.Sprintf("shopper-%02d@shop.test", i),
			password: fmt.Sprintf("shopper-pass-%02d", i),
			ip:       s.randomIP(),
		})
	}
	return s
}

// randomIP returns an address from 203.0.113.0/24 (TEST-NET-3).
func (s *shopper) randomIP() string {
	return fmt.Sprintf("203.0.113.%d", 1+s.rng.IntN(254))
}

// do sends one request and reads the whole answer.
func (s *shopper) do(ctx context.Context, method, path, token, ip string, body any) (reply, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return reply{}, fmt.Errorf("encode request body: %w", err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.cfg.baseURL+path, payload)
	if err != nil {
		return reply{}, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Forwarded-For", ip)

	resp, err := s.client.Do(req)
	if err != nil {
		return reply{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return reply{}, fmt.Errorf("read answer of %s %s: %w", method, path, err)
	}
	return reply{status: resp.StatusCode, body: raw}, nil
}

// call sends a request as the account, then pauses to keep to 1-3 requests a second.
func (s *shopper) call(ctx context.Context, a *account, method, path string, body any) (reply, error) {
	r, err := s.do(ctx, method, path, a.token, a.ip, body)
	if err != nil {
		return reply{}, err
	}
	if r.status == http.StatusUnauthorized {
		a.token = ""
	}
	s.pause(ctx)
	return r, nil
}

func (s *shopper) pause(ctx context.Context) {
	if s.cfg.dryRun || s.fast {
		return
	}
	sleep(ctx, time.Duration(333+s.rng.IntN(667))*time.Millisecond)
}

// sleep waits for d or until ctx ends, and reports whether the full time passed.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *shopper) register(ctx context.Context, a *account) error {
	r, err := s.do(ctx, http.MethodPost, "/auth/register", "", a.ip,
		map[string]string{"email": a.email, "password": a.password})
	if err != nil {
		return err
	}
	// 409 means the account exists from an earlier run.
	if r.status/100 != 2 && r.status != http.StatusConflict {
		return fmt.Errorf("register %s: status %d", a.email, r.status)
	}
	return nil
}

func (s *shopper) signIn(ctx context.Context, a *account) error {
	r, err := s.do(ctx, http.MethodPost, "/auth/login", "", a.ip,
		map[string]string{"email": a.email, "password": a.password})
	if err != nil {
		return err
	}
	if r.status/100 != 2 {
		return fmt.Errorf("sign in %s: status %d", a.email, r.status)
	}
	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		return fmt.Errorf("sign in %s: unreadable answer: %w", a.email, err)
	}
	a.token = cmp.Or(out.Token, out.AccessToken)
	if a.token == "" {
		return fmt.Errorf("sign in %s: no token in the answer", a.email)
	}
	s.pause(ctx)
	return nil
}

func (s *shopper) ensureSignedIn(ctx context.Context, a *account) error {
	if a.token != "" {
		return nil
	}
	return s.signIn(ctx, a)
}

// browse lists the first page of products and remembers their ids.
func (s *shopper) browse(ctx context.Context, a *account) error {
	r, err := s.call(ctx, a, http.MethodGet, "/api/products?limit=20", nil)
	if err != nil {
		return err
	}
	var page struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if json.Unmarshal(r.body, &page) == nil && len(page.Items) > 0 {
		s.products = s.products[:0]
		for _, it := range page.Items {
			s.products = append(s.products, it.ID)
		}
	}
	return nil
}

func (s *shopper) pickProduct() int64 {
	if len(s.products) == 0 {
		return 1
	}
	return s.products[s.rng.IntN(len(s.products))]
}

func (s *shopper) addItem(ctx context.Context, a *account, quantity int) error {
	_, err := s.call(ctx, a, http.MethodPost, "/api/cart/items",
		map[string]any{"product_id": s.pickProduct(), "quantity": quantity})
	return err
}

func checkoutBody(currency, couponCode string) map[string]any {
	body := map[string]any{
		"currency": currency,
		"card":     map[string]string{"number": "4242424242424242", "exp": "12/30", "cvc": "123"},
	}
	if couponCode != "" {
		body["coupon_code"] = couponCode
	}
	return body
}

// checkout places the order. When the server refuses it (sold out, a crash), the cart is
// emptied so the leftovers do not break every later trip of this account.
func (s *shopper) checkout(ctx context.Context, a *account, currency, couponCode string) error {
	r, err := s.call(ctx, a, http.MethodPost, "/api/checkout", checkoutBody(currency, couponCode))
	if err != nil {
		return err
	}
	if r.status/100 == 2 {
		return nil
	}
	return s.emptyCart(ctx, a)
}

// emptyCart sets every line of the account's cart to quantity 0.
func (s *shopper) emptyCart(ctx context.Context, a *account) error {
	r, err := s.call(ctx, a, http.MethodGet, "/api/cart", nil)
	if err != nil {
		return err
	}
	var cart struct {
		Items []struct {
			ProductID int64 `json:"product_id"`
		} `json:"items"`
	}
	if json.Unmarshal(r.body, &cart) != nil {
		return nil
	}
	for _, it := range cart.Items {
		path := fmt.Sprintf("/api/cart/items/%d", it.ProductID)
		if _, err := s.call(ctx, a, http.MethodPatch, path, map[string]int{"quantity": 0}); err != nil {
			return err
		}
	}
	return nil
}

// session is one normal shopping trip: sign in, browse, search, fill the cart, use a
// coupon, check out, look at the orders.
func (s *shopper) session(ctx context.Context, a *account) error {
	if err := s.ensureSignedIn(ctx, a); err != nil {
		return err
	}
	if err := s.browse(ctx, a); err != nil {
		return err
	}
	steps := []func() error{
		func() error {
			_, err := s.call(ctx, a, http.MethodGet, fmt.Sprintf("/api/products/%d", s.pickProduct()), nil)
			return err
		},
		func() error {
			word := searchWords[s.rng.IntN(len(searchWords))]
			_, err := s.call(ctx, a, http.MethodGet, "/api/products?search="+url.QueryEscape(word), nil)
			return err
		},
		func() error { return s.addItem(ctx, a, 1+s.rng.IntN(3)) },
		func() error {
			if s.rng.IntN(2) == 0 {
				return nil
			}
			return s.addItem(ctx, a, 1)
		},
		func() error {
			_, err := s.call(ctx, a, http.MethodPost, "/api/cart/coupon",
				map[string]string{"code": validCoupons[s.rng.IntN(len(validCoupons))]})
			return err
		},
		func() error { return s.checkout(ctx, a, "USD", "") },
		func() error {
			_, err := s.call(ctx, a, http.MethodGet, "/api/orders", nil)
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// incident runs one named incident.
func (s *shopper) incident(ctx context.Context, name string, a *account) error {
	switch name {
	case "api-1":
		return s.withCart(ctx, a, 1, "USD", "VIP25")
	case "api-2":
		probe := injectionProbes[s.rng.IntN(len(injectionProbes))]
		_, err := s.do(ctx, http.MethodGet, "/api/products?search="+url.QueryEscape(probe), "", s.randomIP(), nil)
		return err
	case "api-3":
		_, err := s.do(ctx, http.MethodGet, fmt.Sprintf("/api/products/%d", missingProductID), "", s.randomIP(), nil)
		return err
	case "zero-qty":
		return s.withCart(ctx, a, 0, "USD", "")
	case "uzs":
		return s.withCart(ctx, a, 1, "UZS", "")
	case "brute":
		return s.brute(ctx, a)
	case "scan":
		return s.scan(ctx)
	default:
		return fmt.Errorf("unknown incident %q", name)
	}
}

// withCart signs in, puts quantity units of a product in the cart and checks out.
func (s *shopper) withCart(ctx context.Context, a *account, quantity int, currency, couponCode string) error {
	if err := s.ensureSignedIn(ctx, a); err != nil {
		return err
	}
	if err := s.browse(ctx, a); err != nil {
		return err
	}
	if err := s.addItem(ctx, a, quantity); err != nil {
		return err
	}
	return s.checkout(ctx, a, currency, couponCode)
}

// brute signs in with a wrong password many times from one address.
func (s *shopper) brute(ctx context.Context, a *account) error {
	ip := s.randomIP()
	for i := 0; i < bruteAttempts; i++ {
		_, err := s.do(ctx, http.MethodPost, "/auth/login", "", ip,
			map[string]string{"email": a.email, "password": fmt.Sprintf("wrong-password-%d", i)})
		if err != nil {
			return err
		}
	}
	return nil
}

// scan walks well-known paths, ending with /.env, from one address.
func (s *shopper) scan(ctx context.Context) error {
	ip := s.randomIP()
	for _, path := range scanPaths {
		if _, err := s.do(ctx, http.MethodGet, path, "", ip, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *shopper) incidentDelay() time.Duration {
	every := s.cfg.incidentEvery
	if every < 4 {
		return every
	}
	jitter := time.Duration(s.rng.Int64N(int64(every/2))) - every/4
	return every + jitter
}

func (s *shopper) registerAll(ctx context.Context) error {
	for _, a := range s.accounts {
		for {
			err := s.register(ctx, a)
			if err == nil {
				break
			}
			s.log.Warn("register failed, retrying", "email", a.email, "err", err)
			if !sleep(ctx, 3*time.Second) {
				return ctx.Err()
			}
		}
	}
	return nil
}

func (s *shopper) runLive(ctx context.Context) error {
	if err := s.registerAll(ctx); err != nil {
		return err
	}
	s.log.Info("shopper started", "base_url", s.cfg.baseURL, "incidents", s.cfg.incidents, "every", s.cfg.incidentEvery.String())
	next := time.Now().Add(s.incidentDelay())
	for ctx.Err() == nil {
		a := s.accounts[s.rng.IntN(len(s.accounts))]
		if len(s.cfg.incidents) > 0 && !time.Now().Before(next) {
			name := s.cfg.incidents[s.rng.IntN(len(s.cfg.incidents))]
			s.log.Info("incident", "name", name, "email", a.email)
			if err := s.incident(ctx, name, a); err != nil && ctx.Err() == nil {
				s.log.Warn("incident failed", "name", name, "err", err)
			}
			next = time.Now().Add(s.incidentDelay())
			continue
		}
		if err := s.session(ctx, a); err != nil && ctx.Err() == nil {
			s.log.Warn("session failed", "email", a.email, "err", err)
			sleep(ctx, time.Second)
		}
	}
	return ctx.Err()
}

// runDry prints what one account would send: registration, a shopping trip and every
// selected incident. Nothing goes over the network.
func (s *shopper) runDry(ctx context.Context) error {
	a := s.accounts[0]
	if err := s.register(ctx, a); err != nil {
		return err
	}
	if err := s.session(ctx, a); err != nil {
		return err
	}
	for _, name := range s.cfg.incidents {
		if err := s.incident(ctx, name, a); err != nil {
			return fmt.Errorf("incident %s: %w", name, err)
		}
	}
	return nil
}

func run(ctx context.Context, args []string, lookup func(string) (string, bool), stdout io.Writer) error {
	cfg, err := loadConfig(args, lookup)
	if err != nil {
		return err
	}
	seed := uint64(time.Now().UnixNano())
	if cfg.dryRun {
		return newShopper(cfg, stdout, slog.New(slog.NewJSONHandler(io.Discard, nil)), seed).runDry(ctx)
	}
	log := slog.New(slog.NewJSONHandler(stdout, nil))
	return newShopper(cfg, stdout, log, seed).runLive(ctx)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("shopper stopped", "err", err)
		stop()
		os.Exit(1)
	}
}
