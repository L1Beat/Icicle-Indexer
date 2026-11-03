package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/ava-labs/avalanchego/ids"

	"icicle/pkg/icm"
)

// ICMCounts is a message count split by direction, seen from the C-Chain.
type ICMCounts struct {
	Sent     uint64 `json:"sent"`     // sent from the C-Chain
	Received uint64 `json:"received"` // received on the C-Chain
	Total    uint64 `json:"total"`
}

// ICMChain is the C-Chain's traffic with one other chain.
type ICMChain struct {
	BlockchainID string `json:"blockchain_id"` // CB58
	Name         string `json:"name"`          // from the L1 registry, empty when unlisted
	ICMCounts
}

// ICMOverview is the /metrics/icm payload.
type ICMOverview struct {
	PerpWindow
	Totals  ICMCounts  `json:"totals"`
	ByChain []ICMChain `json:"by_chain"`
}

// ICMDay is one UTC day.
type ICMDay struct {
	Date string `json:"date"`
	ICMCounts
	ByChain []ICMChain `json:"by_chain"`
}

var icmCache = newTTLCache(5 * time.Minute)

// icmWindow resolves the window ending at the last complete UTC day the ICM
// sync has passed, clamped to the day the TeleporterMessenger was deployed.
func (s *Server) icmWindow(ctx context.Context, days int) (PerpWindow, time.Time, error) {
	w := PerpWindow{Days: days}
	through, err := icm.Through(ctx, s.conn)
	if err != nil {
		return w, time.Time{}, err
	}
	asOf := through.Truncate(24*time.Hour).AddDate(0, 0, -1)
	if through.IsZero() || asOf.Before(icm.Start) {
		return w, time.Time{}, nil
	}
	from := asOf.AddDate(0, 0, 1-days)
	if from.Before(icm.Start) {
		from = icm.Start
		w.Days = int(asOf.Sub(icm.Start).Hours()/24) + 1
	}
	a, f := asOf.Format("2006-01-02"), from.Format("2006-01-02")
	w.AsOf, w.From = &a, &f
	return w, asOf, nil
}

// icmChainNames maps CB58 blockchain IDs to their L1 registry names.
func (s *Server) icmChainNames(ctx context.Context) (map[string]string, error) {
	rows, err := s.conn.Query(ctx, `SELECT blockchain_id, name FROM l1_registry FINAL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// icmRow is the message count for one day, direction and other chain.
type icmRow struct {
	day, direction string
	chain          string // CB58
	n              uint64
}

func (s *Server) icmRows(ctx context.Context, asOf time.Time, days int) ([]icmRow, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT toString(toDate(block_time)) AS day, toString(direction), counterparty, count()
		FROM (SELECT * FROM icm_messages FINAL) t
		WHERE block_time >= ? AND block_time < ?
		GROUP BY day, direction, counterparty
		ORDER BY day
	`, asOf.AddDate(0, 0, 1-days), asOf.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []icmRow
	for rows.Next() {
		var r icmRow
		var raw string
		if err := rows.Scan(&r.day, &r.direction, &raw, &r.n); err != nil {
			return nil, err
		}
		var id ids.ID
		copy(id[:], raw)
		r.chain = id.String()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *ICMCounts) add(direction string, n uint64) {
	if direction == "sent" {
		c.Sent += n
	} else {
		c.Received += n
	}
	c.Total += n
}

// icmChains turns per-chain counts into a list, busiest first.
func icmChains(m map[string]*ICMCounts, names map[string]string) []ICMChain {
	out := make([]ICMChain, 0, len(m))
	for id, c := range m {
		out = append(out, ICMChain{BlockchainID: id, Name: names[id], ICMCounts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].BlockchainID < out[j].BlockchainID
	})
	return out
}

// handleICM returns C-Chain ICM message counts
// @Summary C-Chain ICM (Teleporter) messages
// @Description ICM messages sent from and received on the C-Chain over the last N complete UTC days ending as_of, total and per other chain (destination for sends, source for receives). Counted from the official TeleporterMessenger's SendCrossChainMessage and ReceiveCrossChainMessage events, one per messageID in each direction, from 2024-03-05. Messages between two L1s that never touch the C-Chain are not included. Cached for 5 minutes.
// @Tags Metrics - ICM
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(30)
// @Success 200 {object} Response{data=ICMOverview}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm [get]
func (s *Server) handleICM(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 30, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := icmCache.get(r.Context(), fmt.Sprintf("overview:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadICM(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadICM(ctx context.Context, days int) (ICMOverview, error) {
	out := ICMOverview{ByChain: []ICMChain{}}
	win, asOf, err := s.icmWindow(ctx, days)
	out.PerpWindow = win
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.icmRows(ctx, asOf, win.Days)
	if err != nil {
		return out, err
	}
	names, err := s.icmChainNames(ctx)
	if err != nil {
		return out, err
	}
	chains := map[string]*ICMCounts{}
	for _, r := range rows {
		out.Totals.add(r.direction, r.n)
		if chains[r.chain] == nil {
			chains[r.chain] = &ICMCounts{}
		}
		chains[r.chain].add(r.direction, r.n)
	}
	out.ByChain = icmChains(chains, names)
	return out, nil
}

// handleICMTimeseries returns daily C-Chain ICM message counts
// @Summary C-Chain ICM (Teleporter) messages per day
// @Description Messages sent from and received on the C-Chain per UTC day, total and per other chain, for the last N complete days ending as_of. Oldest first, every day included (zero when quiet), from 2024-03-05. Cached for 5 minutes.
// @Tags Metrics - ICM
// @Produce json
// @Param days query int false "Complete UTC days, 1-3650" default(90)
// @Success 200 {object} Response{data=[]ICMDay}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/icm/timeseries [get]
func (s *Server) handleICMTimeseries(w http.ResponseWriter, r *http.Request) {
	days, err := dexDays(r, 90, 3650)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, err.Error())
		return
	}
	v, err := icmCache.get(r.Context(), fmt.Sprintf("timeseries:%d", days), func(ctx context.Context) (interface{}, error) {
		return s.loadICMTimeseries(ctx, days)
	})
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, Response{Data: v})
}

func (s *Server) loadICMTimeseries(ctx context.Context, days int) ([]ICMDay, error) {
	out := []ICMDay{}
	win, asOf, err := s.icmWindow(ctx, days)
	if err != nil || win.AsOf == nil {
		return out, err
	}
	rows, err := s.icmRows(ctx, asOf, win.Days)
	if err != nil {
		return nil, err
	}
	names, err := s.icmChainNames(ctx)
	if err != nil {
		return nil, err
	}
	byDay := map[string]map[string]*ICMCounts{}
	for _, r := range rows {
		if byDay[r.day] == nil {
			byDay[r.day] = map[string]*ICMCounts{}
		}
		if byDay[r.day][r.chain] == nil {
			byDay[r.day][r.chain] = &ICMCounts{}
		}
		byDay[r.day][r.chain].add(r.direction, r.n)
	}
	for d := asOf.AddDate(0, 0, 1-win.Days); !d.After(asOf); d = d.AddDate(0, 0, 1) {
		day := ICMDay{Date: d.Format("2006-01-02")}
		day.ByChain = icmChains(byDay[day.Date], names)
		for _, c := range day.ByChain {
			day.Sent += c.Sent
			day.Received += c.Received
			day.Total += c.Total
		}
		out = append(out, day)
	}
	return out, nil
}
