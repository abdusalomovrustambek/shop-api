package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

// dryRun runs shopper -dry-run with the given environment. The base URL points at a
// host that does not resolve, so any real connection attempt would fail the run.
func dryRun(t *testing.T, env map[string]string) string {
	t.Helper()
	if _, ok := env["SHOPPER_BASE_URL"]; !ok {
		env["SHOPPER_BASE_URL"] = "http://gateway.invalid:1"
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-dry-run"}, lookupFrom(env), &out); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	return out.String()
}

func TestDryRunPrintsTheShoppingFlowAndIncidents(t *testing.T) {
	out := dryRun(t, map[string]string{})
	base := "http://gateway.invalid:1"

	for _, want := range []string{
		"POST " + base + "/auth/register",
		"POST " + base + "/auth/login",
		"GET " + base + "/api/products?limit=20",
		"GET " + base + "/api/products?search=",
		"POST " + base + "/api/cart/items",
		"POST " + base + "/api/cart/coupon",
		"POST " + base + "/api/checkout",
		"GET " + base + "/api/orders",
		// incidents
		`"coupon_code":"VIP25"`,
		"search=%27+",
		"/api/products/999999",
		`"quantity":0`,
		`"currency":"UZS"`,
		"/.env",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output lacks %q", want)
		}
	}
	// One sign-in for the trip plus the wrong-password attempts of the brute-force incident.
	if n := strings.Count(out, "/auth/login"); n != 1+bruteAttempts {
		t.Errorf("%d sign-in requests, want %d", n, 1+bruteAttempts)
	}
	if !strings.Contains(out, "wrong-password-0") {
		t.Error("the brute-force incident does not use wrong passwords")
	}
}

func TestIncidentsAreFilteredByTheEnvironment(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"unset means all", map[string]string{}, allIncidents},
		{"empty means none", map[string]string{"SHOPPER_INCIDENTS": ""}, nil},
		{"blank means none", map[string]string{"SHOPPER_INCIDENTS": "  "}, nil},
		{"a list", map[string]string{"SHOPPER_INCIDENTS": "api-1, brute"}, []string{"api-1", "brute"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadConfig(nil, lookupFrom(tt.env))
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if !slices.Equal(cfg.incidents, tt.want) {
				t.Errorf("incidents = %v, want %v", cfg.incidents, tt.want)
			}
		})
	}

	if _, err := loadConfig(nil, lookupFrom(map[string]string{"SHOPPER_INCIDENTS": "api-1,nope"})); err == nil {
		t.Error("an unknown incident name was accepted")
	}
}

func TestDryRunOnlyRunsTheSelectedIncidents(t *testing.T) {
	out := dryRun(t, map[string]string{"SHOPPER_INCIDENTS": "api-3"})
	if !strings.Contains(out, "/api/products/999999") {
		t.Error("the selected incident api-3 did not run")
	}
	for _, other := range []string{"VIP25", "UZS", "wrong-password", "/.env", "search=%27+"} {
		if strings.Contains(out, other) {
			t.Errorf("output contains %q from an incident that was not selected", other)
		}
	}

	quiet := dryRun(t, map[string]string{"SHOPPER_INCIDENTS": ""})
	for _, other := range []string{"VIP25", "UZS", "wrong-password", "/.env", "999999", "search=%27+"} {
		if strings.Contains(quiet, other) {
			t.Errorf("with incidents off the output still contains %q", other)
		}
	}
	if !strings.Contains(quiet, "/api/checkout") {
		t.Error("with incidents off the normal shopping trip is missing")
	}
}

func TestForwardedAddressesStayInTheTestNet(t *testing.T) {
	testNet := netip.MustParsePrefix("203.0.113.0/24")

	s := newShopper(config{baseURL: defaultBaseURL}, io.Discard, slog.New(slog.NewJSONHandler(io.Discard, nil)), 7)
	for i := 0; i < 2000; i++ {
		addr, err := netip.ParseAddr(s.randomIP())
		if err != nil || !testNet.Contains(addr) {
			t.Fatalf("address %v is not inside %v (%v)", addr, testNet, err)
		}
	}

	out := dryRun(t, map[string]string{})
	matches := regexp.MustCompile(`xff=(\S+)`).FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		t.Fatal("no request carries X-Forwarded-For")
	}
	for _, m := range matches {
		addr, err := netip.ParseAddr(m[1])
		if err != nil || !testNet.Contains(addr) {
			t.Errorf("request sent from %q, outside %v", m[1], testNet)
		}
	}
	if n := strings.Count(out, "\n"); n != len(matches) {
		t.Errorf("%d requests but %d carry X-Forwarded-For", n, len(matches))
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	cfg, err := loadConfig(nil, lookupFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.baseURL != "http://auth-gateway:8080" || cfg.incidentEvery != time.Minute || cfg.dryRun {
		t.Errorf("defaults = %+v", cfg)
	}

	cfg, err = loadConfig([]string{"-dry-run"}, lookupFrom(map[string]string{
		"SHOPPER_BASE_URL":       "http://example.test/",
		"SHOPPER_INCIDENT_EVERY": "5s",
	}))
	if err != nil || cfg.baseURL != "http://example.test" || cfg.incidentEvery != 5*time.Second || !cfg.dryRun {
		t.Errorf("config = %+v, err %v", cfg, err)
	}

	for _, bad := range []string{"soon", "0s", "-5s"} {
		if _, err := loadConfig(nil, lookupFrom(map[string]string{"SHOPPER_INCIDENT_EVERY": bad})); err == nil {
			t.Errorf("SHOPPER_INCIDENT_EVERY=%q was accepted", bad)
		}
	}
}

func TestIncidentDelayIsJittered(t *testing.T) {
	s := newShopper(config{incidentEvery: time.Minute}, io.Discard, slog.New(slog.NewJSONHandler(io.Discard, nil)), 1)
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		d := s.incidentDelay()
		if d < 45*time.Second || d > 75*time.Second {
			t.Fatalf("delay %v is outside 60s +-25%%", d)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Error("the delay never varies")
	}
}
