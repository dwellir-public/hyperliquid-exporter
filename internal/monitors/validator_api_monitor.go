package monitors

import (
	"context"
	"maps"
	"strings"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	hyperliquidapi "github.com/validaoxyz/hyperliquid-exporter/internal/hyperliquid-api"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/safego"
)

var hlResolver *hyperliquidapi.Resolver

func StartValidatorMonitor(ctx context.Context, cfg config.Config, errCh chan<- error) {
	// init HL resolver
	hlResolver = hyperliquidapi.NewResolver(cfg.Chain)

	safego.Go("consensus", func() {
		known := make(map[string]struct{})

		// run immediately on startup to populate mappings
		if err := updateValidatorMetrics(ctx, cfg, known); err != nil {
			logger.Error("Initial validator monitor update error: %v", err)
			errCh <- err
		}

		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := updateValidatorMetrics(ctx, cfg, known); err != nil {
					logger.Error("Validator monitor error: %v", err)
					errCh <- err
				}
			}
		}
	})
}

// known holds the validators seen in the previous snapshot; validators that
// disappear have their per-validator series removed instead of freezing.
func updateValidatorMetrics(ctx context.Context, cfg config.Config, known map[string]struct{}) error {
	// use resolver to get val summaries
	summaries, err := hlResolver.GetValidatorSummaries(ctx, false)
	if err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(summaries))

	totalStake := 0.0
	jailedStake := 0.0
	notJailedStake := 0.0
	activeStake := 0.0
	inactiveStake := 0.0
	mappingCount := 0

	for _, summary := range summaries {
		seen[summary.Validator] = struct{}{}

		// register signer->val mapping (lowercase for consistency)
		metrics.RegisterSignerMapping(strings.ToLower(summary.Signer), strings.ToLower(summary.Validator))
		mappingCount++

		// register the full validator addr for expansion
		metrics.RegisterFullAddress(strings.ToLower(summary.Validator))
		// also register the signer addr for expansion (consensus logs use signer addrss)
		metrics.RegisterFullAddress(strings.ToLower(summary.Signer))

		// register val info (signer and name) for consensus metrics
		metrics.RegisterValidatorInfo(strings.ToLower(summary.Validator), strings.ToLower(summary.Signer), summary.Name)

		metrics.SetValidatorStake(summary.Validator, summary.Signer, summary.Name, summary.Stake)

		// update val jailed status
		jailedStatus := 0.0
		if summary.IsJailed {
			jailedStatus = 1.0
			jailedStake += summary.Stake
		} else {
			notJailedStake += summary.Stake
		}
		metrics.SetValidatorJailedStatus(summary.Validator, summary.Signer, summary.Name, jailedStatus)

		// update active/inactive stake
		if summary.IsActive {
			activeStake += summary.Stake
		} else {
			inactiveStake += summary.Stake
		}

		// set active status
		activeStatus := 0.0
		if summary.IsActive {
			activeStatus = 1.0
		}
		metrics.SetValidatorActiveStatus(summary.Validator, summary.Signer, summary.Name, activeStatus)

		totalStake += summary.Stake
	}

	// update aggregate metrics
	metrics.SetTotalStake(totalStake)
	metrics.SetJailedStake(jailedStake)
	metrics.SetNotJailedStake(notJailedStake)
	metrics.SetActiveStake(activeStake)
	metrics.SetInactiveStake(inactiveStake)
	metrics.SetValidatorCount(int64(len(summaries)))

	dropMissing(known, seen, metrics.RemoveValidatorSeries)
	return nil
}

// calls remove for every key in known that is absent from seen, then
// replaces known's contents with seen. An empty snapshot is treated as a
// failed read rather than an empty set so one bad poll cannot wipe every series.
func dropMissing(known, seen map[string]struct{}, remove func(string)) {
	if len(seen) == 0 {
		return
	}
	for k := range known {
		if _, ok := seen[k]; !ok {
			remove(k)
			logger.DebugComponent("consensus", "Validator %s left the set, removing series", k)
		}
	}
	clear(known)
	maps.Copy(known, seen)
}

// returns the HL resolver instance
func GetValidatorResolver() *hyperliquidapi.Resolver {
	return hlResolver
}
