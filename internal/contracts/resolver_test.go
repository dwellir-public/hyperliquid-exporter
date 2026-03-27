package contracts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/cache"
)

// testResolver builds a resolver without worker goroutines.
func testResolver(t *testing.T, srv *httptest.Server) *Resolver {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return &Resolver{
		cache:      cache.NewLRUCache(5000, 24*time.Hour),
		client:     srv.Client(),
		baseURL:    srv.URL,
		fetchQueue: make(chan string, 100),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// --- Cache behavior ---

func TestGetContractInfoCacheHit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("should not hit server on cache hit")
	}))
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	r.cache.Set("0xabcd", &ContractInfo{
		Address: "0xabcd",
		Name:    "MyToken",
		IsToken: true,
		Symbol:  "MTK",
	})

	info := r.GetContractInfo("0xABCD") // case-insensitive
	if info.Name != "MyToken" {
		t.Errorf("Name = %q, want MyToken", info.Name)
	}
	if info.Symbol != "MTK" {
		t.Errorf("Symbol = %q, want MTK", info.Symbol)
	}
}

func TestGetContractInfoCacheMiss(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	info := r.GetContractInfo("0xdead")

	if info.Name != "unknown" {
		t.Errorf("expected unknown placeholder, got %q", info.Name)
	}

	// should be queued
	select {
	case addr := <-r.fetchQueue:
		if addr != "0xdead" {
			t.Errorf("queued %q, want 0xdead", addr)
		}
	default:
		t.Error("expected address to be queued")
	}
}

func TestGetCacheSize(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	r.cache.Set("0x1", &ContractInfo{})
	r.cache.Set("0x2", &ContractInfo{})

	if r.GetCacheSize() != 2 {
		t.Errorf("cache size = %d, want 2", r.GetCacheSize())
	}
}

// --- Token/contract caching helpers ---

func TestCacheToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	r.cacheToken(&TokenResponse{
		Address: "0xABCD",
		Name:    "Test Token",
		Symbol:  "TT",
		Type:    "ERC-20",
	})

	val, ok := r.cache.Get("0xabcd")
	if !ok {
		t.Fatal("expected cache entry")
	}
	info := val.(*ContractInfo)
	if !info.IsToken || info.Symbol != "TT" || info.Type != "ERC-20" {
		t.Errorf("got %+v", info)
	}
}

func TestCacheSmartContract(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	r.cacheSmartContract("0xABCD", "MyContract")

	val, ok := r.cache.Get("0xabcd")
	if !ok {
		t.Fatal("expected cache entry")
	}
	info := val.(*ContractInfo)
	if info.IsToken || info.Name != "MyContract" || info.Type != "smart-contract" {
		t.Errorf("got %+v", info)
	}
}

func TestCacheTokenNormalizesAddress(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	r.cacheToken(&TokenResponse{Address: "0xAABBCC"})

	_, ok := r.cache.Get("0xaabbcc")
	if !ok {
		t.Error("expected lowercase key in cache")
	}
}

// --- Fetch + cache integration ---

func TestFetchAndCacheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tokens/") {
			_ = json.NewEncoder(w).Encode(TokenResponse{
				Address: "0xabcd",
				Name:    "Fetched Token",
				Symbol:  "FT",
				Type:    "ERC-20",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	r.fetchAndCacheContract("0xabcd")

	val, ok := r.cache.Get("0xabcd")
	if !ok {
		t.Fatal("expected cached entry after fetch")
	}
	info := val.(*ContractInfo)
	if info.Name != "Fetched Token" || !info.IsToken {
		t.Errorf("got %+v", info)
	}
}

func TestFetchAndCacheSmartContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tokens/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/smart-contracts/") {
			_ = json.NewEncoder(w).Encode(SmartContractResponse{
				Address: struct {
					Hash string `json:"hash"`
					Name string `json:"name"`
				}{Hash: "0xabcd", Name: "MyContract"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	r.fetchAndCacheContract("0xabcd")

	val, ok := r.cache.Get("0xabcd")
	if !ok {
		t.Fatal("expected cached entry")
	}
	info := val.(*ContractInfo)
	if info.IsToken || info.Name != "MyContract" {
		t.Errorf("got %+v", info)
	}
}

func TestFetchAndCacheUnknown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)
	r.fetchAndCacheContract("0xdead")

	val, ok := r.cache.Get("0xdead")
	if !ok {
		t.Fatal("expected 'unknown' to be cached to prevent re-fetch")
	}
	info := val.(*ContractInfo)
	if info.Name != "unknown" {
		t.Errorf("Name = %q, want unknown", info.Name)
	}
}

// --- Worker pool ---

func TestFetchWorkerProcessesQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tokens/") {
			_ = json.NewEncoder(w).Encode(TokenResponse{
				Address: "0xabcd",
				Name:    "Async Token",
				Symbol:  "AT",
				Type:    "ERC-20",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)

	// fetchWorker calls r.wg.Done(), so we must use r.wg
	r.wg.Add(1)
	go r.fetchWorker()

	r.fetchQueue <- "0xabcd"

	// poll for result
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := r.cache.Get("0xabcd"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	r.cancel()
	r.wg.Wait()

	val, ok := r.cache.Get("0xabcd")
	if !ok {
		t.Fatal("worker should have fetched and cached the contract")
	}
	info := val.(*ContractInfo)
	if info.Name != "Async Token" {
		t.Errorf("Name = %q, want Async Token", info.Name)
	}
}

func TestFetchWorkerDedup(t *testing.T) {
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		if strings.HasPrefix(r.URL.Path, "/tokens/") {
			_ = json.NewEncoder(w).Encode(TokenResponse{
				Address: "0xabcd",
				Name:    "Token",
				Symbol:  "T",
				Type:    "ERC-20",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	r := testResolver(t, srv)

	r.wg.Add(1)
	go r.fetchWorker()

	// send same address twice
	r.fetchQueue <- "0xabcd"
	// wait for first to be processed
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := r.cache.Get("0xabcd"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.fetchQueue <- "0xabcd" // duplicate — should be deduped by seen map
	time.Sleep(50 * time.Millisecond)

	r.cancel()
	r.wg.Wait()

	// token endpoint serves both /tokens/0xabcd requests, but only 1 should happen
	if reqCount.Load() != 1 {
		t.Errorf("request count = %d, want 1 (dedup should prevent second fetch)", reqCount.Load())
	}
}

func TestShutdown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	r := &Resolver{
		cache:      cache.NewLRUCache(100, time.Hour),
		client:     srv.Client(),
		baseURL:    srv.URL,
		fetchQueue: make(chan string, 100),
		ctx:        ctx,
		cancel:     cancel,
	}

	// start workers like production
	const numWorkers = 3
	for range numWorkers {
		r.wg.Add(1)
		go r.fetchWorker()
	}

	// shutdown should complete without hanging
	done := make(chan struct{})
	go func() {
		r.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown timed out — possible goroutine leak")
	}
}
