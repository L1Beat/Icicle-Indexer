package lending

import "github.com/prometheus/client_golang/prometheus"

// Prometheus metrics for the lending engine. Registered on the default registry,
// which the service exposes over /metrics.
var (
	metricPositionsTracked = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "icicle_lending_positions_tracked",
		Help: "Tracked positions by protocol and refresh tier",
	}, []string{"protocol", "tier"})

	metricLiquidatable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "icicle_lending_liquidatable",
		Help: "Currently liquidatable positions by protocol",
	}, []string{"protocol"})

	metricRefreshSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "icicle_lending_refresh_seconds",
		Help:    "Health refresh duration by trigger",
		Buckets: prometheus.DefBuckets,
	}, []string{"trigger"})

	metricRecomputeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "icicle_lending_recompute_total",
		Help: "Accounts recomputed by trigger",
	}, []string{"trigger"})

	metricMulticallTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "icicle_lending_multicall_requests_total",
		Help: "Multicall3 aggregate3 requests issued",
	})
)

func init() {
	prometheus.MustRegister(
		metricPositionsTracked,
		metricLiquidatable,
		metricRefreshSeconds,
		metricRecomputeTotal,
		metricMulticallTotal,
	)
}

// updateGauges recomputes tier and liquidatable gauges from the in-memory cache.
func (e *HealthEngine) updateGauges() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for proto, accounts := range e.state {
		counts := map[Tier]float64{TierHot: 0, TierWarm: 0, TierCold: 0}
		var liq float64
		for _, st := range accounts {
			counts[st.tier]++
			if st.liquidatable {
				liq++
			}
		}
		for tier, n := range counts {
			metricPositionsTracked.WithLabelValues(string(proto), string(tier)).Set(n)
		}
		metricLiquidatable.WithLabelValues(string(proto)).Set(liq)
	}
}
