package hyperliquidapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

var testSummaries = []ValidatorSummary{
	{
		Validator: "0xAABB",
		Signer:    "0xCCDD",
		Name:      "alpha",
		Stake:     1000,
		IsJailed:  false,
		IsActive:  true,
	},
	{
		Validator: "0xEEFF",
		Signer:    "0x1122",
		Name:      "beta",
		Stake:     2000,
		IsJailed:  true,
		IsActive:  false,
	},
}

// testResolver creates a resolver backed by an httptest server.
// The handler receives the request count and can customize responses.
func testResolver(t *testing.T, handler http.HandlerFunc) *Resolver {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	r := NewResolver("mainnet")
	r.baseURL = srv.URL
	r.client = srv.Client()
	return r
}

// validatorHandler returns a handler that serves testSummaries and counts requests.
func validatorHandler(count *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(testSummaries)
	}
}

// --- NewResolver ---

func TestNewResolverMainnet(t *testing.T) {
	r := NewResolver("mainnet")
	if r.baseURL != "https://api.hyperliquid.xyz" {
		t.Errorf("baseURL = %q", r.baseURL)
	}
}

func TestNewResolverTestnet(t *testing.T) {
	r := NewResolver("testnet")
	if r.baseURL != "https://api.hyperliquid-testnet.xyz" {
		t.Errorf("baseURL = %q", r.baseURL)
	}
}

func TestGetChainAndBaseURL(t *testing.T) {
	r := NewResolver("mainnet")
	if r.GetChain() != "mainnet" {
		t.Errorf("GetChain() = %q", r.GetChain())
	}
	if r.GetBaseURL() != "https://api.hyperliquid.xyz" {
		t.Errorf("GetBaseURL() = %q", r.GetBaseURL())
	}
}

// --- Cache + lookups ---

func TestGetValidatorBySigner(t *testing.T) {
	r := NewResolver("mainnet")
	r.updateValidatorCache(testSummaries)

	// case-insensitive lookup
	v, ok := r.GetValidatorBySigner("0xccdd")
	if !ok || v == nil {
		t.Fatal("expected hit")
	}
	if v.Name != "alpha" {
		t.Errorf("Name = %q, want alpha", v.Name)
	}
}

func TestGetValidatorBySignerMiss(t *testing.T) {
	r := NewResolver("mainnet")
	_, ok := r.GetValidatorBySigner("0xdead")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestGetValidatorByAddress(t *testing.T) {
	r := NewResolver("mainnet")
	r.updateValidatorCache(testSummaries)

	v, ok := r.GetValidatorByAddress("0xAABB")
	if !ok || v == nil {
		t.Fatal("expected hit")
	}
	if v.Stake != 1000 {
		t.Errorf("Stake = %f, want 1000", v.Stake)
	}
}

func TestGetValidatorByAddressMiss(t *testing.T) {
	r := NewResolver("mainnet")
	_, ok := r.GetValidatorByAddress("0xdead")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestGetSignerToValidatorMapping(t *testing.T) {
	r := NewResolver("mainnet")
	r.updateValidatorCache(testSummaries)

	m := r.GetSignerToValidatorMapping()
	if len(m) != 2 {
		t.Fatalf("len = %d, want 2", len(m))
	}

	// mutating the copy should not affect internal state
	m["injected"] = "bad"
	m2 := r.GetSignerToValidatorMapping()
	if _, exists := m2["injected"]; exists {
		t.Fatal("returned map should be a copy")
	}
}

// --- GetValidatorSummaries ---

func TestGetValidatorSummariesFresh(t *testing.T) {
	var count atomic.Int32
	r := testResolver(t, validatorHandler(&count))

	summaries, err := r.GetValidatorSummaries(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2", len(summaries))
	}
	if summaries[0].Name != "alpha" {
		t.Errorf("Name = %q, want alpha", summaries[0].Name)
	}
	if count.Load() != 1 {
		t.Errorf("request count = %d, want 1", count.Load())
	}
}

func TestGetValidatorSummariesCached(t *testing.T) {
	var count atomic.Int32
	r := testResolver(t, validatorHandler(&count))

	// first call populates cache
	_, _ = r.GetValidatorSummaries(context.Background(), false)

	// second call within 1min should use cache
	summaries, err := r.GetValidatorSummaries(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatal("expected cached summaries")
	}
	if count.Load() != 1 {
		t.Errorf("request count = %d, want 1 (second call should use cache)", count.Load())
	}
}

func TestGetValidatorSummariesForceRefresh(t *testing.T) {
	var count atomic.Int32
	r := testResolver(t, validatorHandler(&count))

	_, _ = r.GetValidatorSummaries(context.Background(), false)
	_, _ = r.GetValidatorSummaries(context.Background(), true) // force

	if count.Load() != 2 {
		t.Errorf("request count = %d, want 2 (force should bypass cache)", count.Load())
	}
}

func TestGetValidatorSummariesStaleFallback(t *testing.T) {
	var count atomic.Int32
	r := testResolver(t, func(w http.ResponseWriter, req *http.Request) {
		n := count.Add(1)
		if n == 1 {
			// first call succeeds
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(testSummaries)
		} else {
			// subsequent calls fail
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	// populate cache
	_, _ = r.GetValidatorSummaries(context.Background(), false)

	// expire cache by backdating lastUpdate
	r.mu.Lock()
	r.validatorCache.lastUpdate = time.Now().Add(-2 * time.Minute)
	r.mu.Unlock()

	// use short timeout to avoid waiting for all 3 retry backoffs
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// should fall back to stale cache
	summaries, err := r.GetValidatorSummaries(ctx, false)
	if err != nil {
		t.Fatalf("expected stale fallback, got error: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2 from stale cache", len(summaries))
	}
}

func TestGetValidatorSummariesNoCache500(t *testing.T) {
	r := testResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	// use short timeout to avoid waiting for all 3 retry backoffs
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := r.GetValidatorSummaries(ctx, false)
	if err == nil {
		t.Fatal("expected error when no cache and server fails")
	}
}
