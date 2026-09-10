package metrics

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"
)

// registers all observable instruments with a single callback
func RegisterCallbacks() error {
	// callback for metrics
	callback, err := meter.RegisterCallback(
		func(ctx context.Context, o api.Observer) error {
			metricsMutex.RLock()
			defer metricsMutex.RUnlock()

			commonLabels := getCommonLabels()

			//  regular values with common labels
			for instrument, value := range currentValues {
				switch v := value.(type) {
				case float64:
					o.ObserveFloat64(instrument.(api.Float64Observable), v, api.WithAttributes(commonLabels...))
				case int64:
					o.ObserveInt64(instrument.(api.Int64Observable), v, api.WithAttributes(commonLabels...))
				}
			}

			// labeled values
			for instrument, values := range labeledValues {
				for _, v := range values {
					allLabels := slices.Concat(v.labels, commonLabels)
					switch obs := instrument.(type) {
					case api.Float64Observable:
						o.ObserveFloat64(obs, v.value, api.WithAttributes(allLabels...))
					case api.Int64Observable:
						o.ObserveInt64(obs, int64(v.value), api.WithAttributes(allLabels...))
					}
				}
			}

			// vote age is derived at scrape time from the last observed vote
			now := time.Now()
			for _, v := range lastVotes {
				allLabels := slices.Concat(v.labels, commonLabels)
				o.ObserveFloat64(HLConsensusVoteTimeDiffGauge, now.Sub(v.updatedAt).Seconds(), api.WithAttributes(allLabels...))
			}

			for stream, at := range sourceSamples {
				// sample times come from log timestamps; clock skew must not go negative
				o.ObserveFloat64(HLExporterSourceSampleAgeGauge, max(now.Sub(at).Seconds(), 0),
					api.WithAttributes(append([]attribute.KeyValue{attribute.String("stream", stream)}, commonLabels...)...))
			}

			// collect memory stats
			if err := collectMemoryStats(ctx, o); err != nil {
				return err
			}

			return nil
		},
		getAllObservables()...,
	)
	if err != nil {
		return fmt.Errorf("failed to register callbacks: %w", err)
	}

	callbacks = append(callbacks, callback)
	return nil
}
