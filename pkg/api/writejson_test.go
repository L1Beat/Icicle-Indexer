package api

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// A value JSON cannot carry must not produce an empty 200.
func TestWriteJSON_UnencodableIs500(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, Response{Data: map[string]float64{"v": math.NaN()}})
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Body.String(), "INTERNAL_ERROR")

	w = httptest.NewRecorder()
	writeJSON(w, http.StatusOK, Response{Data: 1})
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"data":1}`, w.Body.String())
}
