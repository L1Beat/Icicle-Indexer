package evmindexer

import (
	"fmt"
	"log/slog"
	"time"
)

var epoch = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// processGranularMetrics checks and runs all granular metrics
func (r *IndexRunner) processGranularMetrics(latestBlockTime time.Time) {
	for _, metricFile := range r.granularMetrics {
		for _, granularity := range []string{"hour", "day", "week", "month"} {
			// Use just the metric filename for indexer name, granularity tracked separately
			indexerName := fmt.Sprintf("evm_metrics/%s", metricFile)

			watermark := r.getWatermarkWithGranularity(indexerName, granularity)

			// Initialize to epoch if never run
			lastPeriod := watermark.LastPeriod
			if lastPeriod.IsZero() {
				lastPeriod = epoch
			}

			// Calculate periods to process
			periods := getPeriodsToProcess(lastPeriod, latestBlockTime, granularity)
			if len(periods) == 0 {
				continue
			}

			// Run metric
			start := time.Now()
			if err := r.runGranularMetric(metricFile, granularity, periods); err != nil {
				slog.Error("Failed to run granular metric, will retry next cycle", "chain_id", r.chainId, "indexer", indexerName, "granularity", granularity, "error", err)
				continue // Skip to next granularity, watermark not updated so will retry
			}
			elapsed := time.Since(start)
			slog.Info("Granular metric processed", "chain_id", r.chainId, "indexer", indexerName, "granularity", granularity, "periods", len(periods), "elapsed", elapsed)

			watermarkToSave := *watermark
			watermarkToSave.LastPeriod = periods[len(periods)-1]
			if err := r.saveWatermarkWithGranularity(indexerName, granularity, &watermarkToSave); err != nil {
				slog.Error("Failed to save watermark, will retry next cycle", "chain_id", r.chainId, "indexer", indexerName, "granularity", granularity, "error", err)
				// Leave in-memory watermark unchanged so the next cycle retries the range.
				continue
			}
			watermark.LastPeriod = watermarkToSave.LastPeriod
		}
	}
}

// runGranularMetric executes a single granular metric for given periods
func (r *IndexRunner) runGranularMetric(metricFile string, granularity string, periods []time.Time) error {
	firstPeriod := periods[0]
	lastPeriod := nextPeriod(periods[len(periods)-1], granularity) // exclusive end

	// Template parameters (string replacement)
	templateParams := []struct{ key, value string }{
		{"{chain_id}", fmt.Sprintf("%d", r.chainId)},
		{"{granularity}", granularity},
		{"{granularityCamelCase}", capitalize(granularity)},
	}

	// Bind parameters (native ClickHouse parameter binding for WHERE clauses)
	bindParams := map[string]interface{}{
		"chain_id":     r.chainId,
		"first_period": firstPeriod,
		"last_period":  lastPeriod,
	}

	filename := fmt.Sprintf("evm_metrics/%s.sql", metricFile)
	return executeSQLFile(r.conn, r.sqlDir, filename, templateParams, bindParams)
}
