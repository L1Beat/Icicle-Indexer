package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func loadOpenAPISpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(openAPISpec)
	require.NoError(t, err)
	require.NoError(t, doc.Validate(context.Background()))
	return doc
}

func TestOpenAPISpecIsValid(t *testing.T) {
	doc := loadOpenAPISpec(t)
	require.Equal(t, "https://api.l1beat.io", doc.Servers[0].URL)
	require.Len(t, doc.Paths.Map(), 54)

	// /api/v1/treasuries comes from the L1Beat backend's own spec.
	treasuries := doc.Paths.Value("/api/v1/treasuries")
	require.NotNil(t, treasuries)
	require.Equal(t, "backend", treasuries.Extensions["x-l1beat-service"])
	require.NotNil(t, doc.Components.Schemas["Treasuries"])

	// Internal endpoints must stay out of the public spec.
	for _, internal := range []string{
		"/api/v1/data/delegators/{address}/delegations",
		"/api/v1/metrics/storage",
	} {
		require.Nil(t, doc.Paths.Value(internal), internal)
	}

	// Nullable fields are marked so generated clients allow null.
	created := doc.Components.Schemas["api.ChainInfo"].Value.Properties["created_time"].Value
	require.True(t, created.Nullable)
}

// Every documented path must hit a registered route: a request with sample
// path values must not fall through to the router's own 404.
func TestOpenAPIPathsAreRouted(t *testing.T) {
	doc := loadOpenAPISpec(t)
	server := NewTestServer(&MockConn{})
	param := regexp.MustCompile(`\{[^}]+\}`)
	for path, item := range doc.Paths.Map() {
		if item.Extensions["x-l1beat-service"] != nil {
			continue // served by another service on the same host
		}
		url := param.ReplaceAllString(path, "1")
		w := MakeRequest(t, server, "GET", url)
		require.NotEqual(t, "404 page not found\n", w.Body.String(), path)
	}
}

func TestOpenAPIEndpoint(t *testing.T) {
	server := NewTestServer(&MockConn{})
	w := MakeRequest(t, server, "GET", "/openapi.json")
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "application/json"))
	require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, w.Body.String(), `"openapi": "3.0.3"`)
}
