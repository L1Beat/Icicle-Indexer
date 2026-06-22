package stealtime

import (
	"math/big"
	"sort"

	"github.com/ava-labs/libevm/common"
)

// Observation is one profitable historical liquidation with its reconstructed
// timing and context.
type Observation struct {
	Account      common.Address
	Liquidator   common.Address
	Protocol     string
	StealTime    uint64
	Censored     bool
	NetProfitUSD *big.Int // 1e18-scaled
	SizeBucket   string   // small | medium | large
	TakenBlock   uint64
}

// SizeBucketFor classifies by net profit in USD (1e18-scaled).
func SizeBucketFor(netProfitUSD *big.Int) string {
	if netProfitUSD == nil {
		return "small"
	}
	usd := new(big.Int).Div(netProfitUSD, wad)
	switch {
	case usd.Cmp(big.NewInt(1000)) >= 0:
		return "large"
	case usd.Cmp(big.NewInt(100)) >= 0:
		return "medium"
	default:
		return "small"
	}
}

var wad = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

// Histogram counts steal times into fixed buckets, plus a censored bucket.
type Histogram struct {
	B0       int // exactly 0 blocks
	B1       int
	B2       int
	B3to5    int
	B6to10   int
	B11to20  int
	B21plus  int
	Censored int
}

func (h *Histogram) add(steal uint64, censored bool) {
	if censored {
		h.Censored++
		return
	}
	switch {
	case steal == 0:
		h.B0++
	case steal == 1:
		h.B1++
	case steal == 2:
		h.B2++
	case steal <= 5:
		h.B3to5++
	case steal <= 10:
		h.B6to10++
	case steal <= 20:
		h.B11to20++
	default:
		h.B21plus++
	}
}

// Incumbent is one liquidator's realized share.
type Incumbent struct {
	Liquidator common.Address
	Count      int
}

// Distribution is the aggregate steal-time result.
type Distribution struct {
	Total          int
	Censored       int
	Overall        Histogram
	ByProtocol     map[string]*Histogram
	BySize         map[string]*Histogram
	MedianBlocks   uint64
	P90Blocks      uint64
	WithinTwo      float64 // fraction taken within 0 to 2 blocks (of non-censored)
	BeyondTen      float64 // fraction taken after more than 10 blocks (of non-censored)
	TotalProfit    *big.Int
	TopLiquidators []Incumbent
	TopNShare      float64
}

// Aggregate builds the distribution. Percentiles and the within-two / beyond-ten
// fractions are over non-censored observations; censored ones are reported
// separately so a fast field is not hidden by a long unobservable tail.
func Aggregate(obs []Observation, topN int) Distribution {
	d := Distribution{
		ByProtocol:  map[string]*Histogram{},
		BySize:      map[string]*Histogram{},
		TotalProfit: big.NewInt(0),
	}

	var stealsNonCensored []uint64
	counts := map[common.Address]int{}

	for _, o := range obs {
		d.Total++
		d.Overall.add(o.StealTime, o.Censored)
		hp := d.ByProtocol[o.Protocol]
		if hp == nil {
			hp = &Histogram{}
			d.ByProtocol[o.Protocol] = hp
		}
		hp.add(o.StealTime, o.Censored)
		hs := d.BySize[o.SizeBucket]
		if hs == nil {
			hs = &Histogram{}
			d.BySize[o.SizeBucket] = hs
		}
		hs.add(o.StealTime, o.Censored)

		if o.Censored {
			d.Censored++
		} else {
			stealsNonCensored = append(stealsNonCensored, o.StealTime)
		}
		if o.NetProfitUSD != nil {
			d.TotalProfit.Add(d.TotalProfit, o.NetProfitUSD)
		}
		counts[o.Liquidator]++
	}

	if len(stealsNonCensored) > 0 {
		sort.Slice(stealsNonCensored, func(i, j int) bool { return stealsNonCensored[i] < stealsNonCensored[j] })
		d.MedianBlocks = percentile(stealsNonCensored, 50)
		d.P90Blocks = percentile(stealsNonCensored, 90)
		var within, beyond int
		for _, s := range stealsNonCensored {
			if s <= 2 {
				within++
			}
			if s > 10 {
				beyond++
			}
		}
		n := float64(len(stealsNonCensored))
		d.WithinTwo = float64(within) / n
		d.BeyondTen = float64(beyond) / n
	}

	d.TopLiquidators, d.TopNShare = topLiquidators(counts, d.Total, topN)
	return d
}

func percentile(sorted []uint64, p int) uint64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * (len(sorted) - 1)) / 100
	return sorted[idx]
}

func topLiquidators(counts map[common.Address]int, total, topN int) ([]Incumbent, float64) {
	list := make([]Incumbent, 0, len(counts))
	for a, c := range counts {
		list = append(list, Incumbent{Liquidator: a, Count: c})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Count > list[j].Count })
	if len(list) > topN {
		list = list[:topN]
	}
	var top int
	for _, it := range list {
		top += it.Count
	}
	share := 0.0
	if total > 0 {
		share = float64(top) / float64(total)
	}
	return list, share
}
