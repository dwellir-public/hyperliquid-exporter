package metrics

import (
	"sync"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/cache"
	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"
)

type labeledValue struct {
	value     float64
	labels    []attribute.KeyValue
	updatedAt time.Time
}

type NodeIdentity struct {
	ValidatorAddress string
	ServerIP         string
	Alias            string
	IsValidator      bool
	Chain            string
}

// global vars for metric state management
var (
	nodeIdentity  NodeIdentity
	metricsMutex  sync.RWMutex
	currentValues = make(map[api.Observable]any)
	labeledValues = make(map[api.Observable]map[string]labeledValue)
	// latest vote log time per validator address; value is unused
	lastVotes     = make(map[string]labeledValue)
	callbacks     []api.Registration
	cleanupTicker *time.Ticker
)

// votes older than this are dropped from hl_consensus_vote_time_diff_seconds
const voteMaxAge = 24 * time.Hour

// signerMap maps signer addr -> val address
// using LRU cache
var signerMap *cache.LRUCache

// store additional info about a val
type ValidatorInfo struct {
	Signer string
	Name   string
}

// maps val addr -> val info
var validatorInfoCache *cache.LRUCache

// set val addr for a signer
func RegisterSignerMapping(signer, validator string) {
	if signerMap == nil {
		initSignerMap()
	}
	signerMap.Set(signer, validator)
}

// get val addr for a signer
func GetValidatorForSigner(signer string) (string, bool) {
	if signerMap == nil {
		initSignerMap()
	}
	val, exists := signerMap.Get(signer)
	if !exists {
		return "", false
	}
	return val.(string), true
}

// store val info (signer + name) for a val addr
func RegisterValidatorInfo(validator, signer, name string) {
	if validatorInfoCache == nil {
		initValidatorInfoCache()
	}
	validatorInfoCache.Set(validator, ValidatorInfo{
		Signer: signer,
		Name:   name,
	})
}

// get signer and name for a val addr
func GetValidatorInfo(validator string) (signer string, name string, exists bool) {
	if validatorInfoCache == nil {
		initValidatorInfoCache()
	}

	val, exists := validatorInfoCache.Get(validator)
	if !exists {
		return "", "", false
	}

	info := val.(ValidatorInfo)
	return info.Signer, info.Name, true
}

// init signer map with LRU cache
func initSignerMap() {
	metricsMutex.Lock()
	defer metricsMutex.Unlock()
	if signerMap == nil {
		// init with reasonable size for val count
		signerMap = cache.NewLRUCache(5000, 24*time.Hour)
	}
}

// init val info cache
func initValidatorInfoCache() {
	metricsMutex.Lock()
	defer metricsMutex.Unlock()
	if validatorInfoCache == nil {
		// init with reasonable size for val count
		validatorInfoCache = cache.NewLRUCache(5000, 24*time.Hour)
	}
}

// start periodic pruning of time-bounded metric state
func StartMetricsCleanup() {
	if cleanupTicker != nil {
		return
	}

	cleanupTicker = time.NewTicker(30 * time.Second)
	go func() {
		for range cleanupTicker.C {
			pruneStaleVotes(voteMaxAge)
		}
	}()
}

// stop periodic cleanup
func StopMetricsCleanup() {
	if cleanupTicker != nil {
		cleanupTicker.Stop()
		cleanupTicker = nil
	}
}
