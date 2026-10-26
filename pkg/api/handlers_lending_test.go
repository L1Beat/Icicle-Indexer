package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
)

const lendingAddr = "0x9674fc77a3f5773002877cae189a51ea89cc4316"

// A malformed address filter must be rejected, never silently dropped: dropping it
// widens the result to every account, which a bot could act on as if filtered.
func TestLendingAddressFilter_RejectsMalformed(t *testing.T) {
	server := NewTestServer(&MockConn{})
	for _, path := range []string{
		"/api/v1/data/evm/43114/lending/liquidations?account=0x9674fc77",
		"/api/v1/data/evm/43114/lending/liquidations?account=0xzz74fc77a3f5773002877cae189a51ea89cc4316",
		"/api/v1/data/evm/43114/lending/liquidations?asset=nope",
		"/api/v1/data/evm/43114/lending/positions?asset=0x1234",
		"/api/v1/data/evm/43114/lending/positions?min_debt_base=250usd",
		"/api/v1/data/evm/43114/lending/positions?min_debt_base=-1",
	} {
		w := MakeRequest(t, server, "GET", path)
		AssertErrorResponse(t, w, http.StatusBadRequest, ErrInvalidParameter)
	}
}

func TestAddrFilter(t *testing.T) {
	cases := []struct {
		query string
		want  string
		ok    bool
	}{
		{"", "", true},
		{"account=", "", true},
		{"account=%20", "", true},
		{"account=" + lendingAddr, strings.TrimPrefix(lendingAddr, "0x"), true},
		{"account=" + strings.ToUpper(lendingAddr[2:]), strings.TrimPrefix(lendingAddr, "0x"), true},
		{"account=0x9674fc77", "", false},
		{"account=0xzz74fc77a3f5773002877cae189a51ea89cc4316", "", false},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", "/x?"+c.query, nil)
		got, ok := addrFilter(r, "account")
		assert.Equal(t, c.want, got, c.query)
		assert.Equal(t, c.ok, ok, c.query)
	}
}

// An account with no liquidations returns an empty list, not null.
func TestLendingLiquidations_EmptyIsList(t *testing.T) {
	conn := &MockConn{QueryFunc: func(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
		return NewMockRows(nil, nil), nil
	}}
	server := NewTestServer(conn)
	w := MakeRequest(t, server, "GET", "/api/v1/data/evm/43114/lending/liquidations?account="+lendingAddr)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"data":[]`)
}
