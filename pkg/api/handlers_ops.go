package api

import (
	"net/http"
)

// StorageTable is the on-disk footprint and row count of one ClickHouse table.
type StorageTable struct {
	Table     string `json:"table" example:"raw_txs"`
	SizeBytes uint64 `json:"size_bytes" example:"123456789"`
	Rows      uint64 `json:"rows" example:"1000000000"`
}

// handleStorageStats returns per-table on-disk size and row counts for the
// active parts of the current database. This is operational metadata for the
// sync/status dashboard — table names and sizes only, no row contents.
// @Summary Get storage stats
// @Description Per-table on-disk size (bytes) and row counts, largest first.
// @Tags Metrics - Indexer
// @Produce json
// @Success 200 {object} Response{data=[]StorageTable}
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/metrics/storage [get]
func (s *Server) handleStorageStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := s.conn.Query(ctx, `
		SELECT
			table,
			toUInt64(sum(bytes_on_disk)) AS size_bytes,
			toUInt64(sum(rows))          AS rows
		FROM system.parts
		WHERE active AND database = currentDatabase()
		GROUP BY table
		ORDER BY size_bytes DESC
	`)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	tables := []StorageTable{}
	for rows.Next() {
		var t StorageTable
		if err := rows.Scan(&t.Table, &t.SizeBytes, &t.Rows); err != nil {
			writeInternalError(w, err.Error())
			return
		}
		tables = append(tables, t)
	}

	writeJSON(w, http.StatusOK, Response{Data: tables})
}
