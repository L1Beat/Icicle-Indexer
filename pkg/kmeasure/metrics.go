package kmeasure

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricEvaluated = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "icicle_kmeasure_evaluated", Help: "Liquidatable positions evaluated in the last run",
	})
	metricK = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "icicle_kmeasure_profitable", Help: "Profitable positions (K) in the last run",
	})
	metricQuoterCalls = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "icicle_kmeasure_quoter_calls_total", Help: "Quoter calls issued",
	})
	metricQuoterFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "icicle_kmeasure_quoter_failures_total", Help: "Quoter calls with no usable route",
	})
	metricQuoterLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "icicle_kmeasure_quoter_latency_seconds", Help: "Quoter call latency", Buckets: prometheus.DefBuckets,
	})
)

var registerOnce sync.Once

// registerMetrics registers collectors once. Safe to call on every run.
func registerMetrics() {
	registerOnce.Do(func() {
		prometheus.MustRegister(metricEvaluated, metricK, metricQuoterCalls, metricQuoterFailures, metricQuoterLatency)
	})
}

func observeQuoterLatency(d time.Duration) {
	metricQuoterLatency.Observe(d.Seconds())
}
