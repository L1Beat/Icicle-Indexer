package api

import (
	"net/http"
	"strings"

	"icicle/pkg/enrich"
)

// maxLabelLookup bounds ?addresses= on the labels endpoint.
const maxLabelLookup = 100

// handleLabels returns labels for a batch of addresses
// @Summary Get address labels
// @Description Resolve up to 100 addresses to curated / verified-source / token labels. Unknown addresses are omitted from the map.
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param addresses query string true "Comma-separated 0x addresses (max 100)"
// @Success 200 {object} Response{data=map[string]AddressLabel}
// @Failure 400 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/labels [get]
func (s *Server) handleLabels(w http.ResponseWriter, r *http.Request) {
	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("addresses"))
	if raw == "" {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "addresses query parameter is required")
		return
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxLabelLookup {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "at most 100 addresses per request")
		return
	}
	out := make(map[string]*AddressLabel, len(parts))
	for _, p := range parts {
		addr, ok := enrich.NormalizeAddress(p)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "invalid address: "+strings.TrimSpace(p))
			return
		}
		if label := s.labelFor(chainID, addr); label != nil {
			out[addr] = label
		}
	}
	writeJSON(w, http.StatusOK, Response{Data: out})
}
