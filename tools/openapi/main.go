// Command openapi builds the public OpenAPI 3 spec served at /openapi.json.
//
// It reads the Swagger 2.0 file that swag generates from the handler
// annotations, keeps only the public endpoints, converts it to OpenAPI 3,
// marks pointer fields nullable and adds the shared documentation (units,
// errors, rate-limit headers). Run it through scripts/gen-openapi.sh.
//
//	go run ./tools/openapi <swagger.json> <pkg/api> <out.json>
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
)

// publicPaths are the endpoints documented on the public playground. Anything
// else the API serves is internal and stays out of the spec.
var publicPaths = []string{
	"/health",
	"/api/v1/metrics/indexer/status",
	"/api/v1/data/evm/{chainId}/blocks",
	"/api/v1/data/evm/{chainId}/blocks/{number}",
	"/api/v1/data/evm/{chainId}/txs",
	"/api/v1/data/evm/{chainId}/txs/{hash}",
	"/api/v1/data/evm/{chainId}/address/{address}/txs",
	"/api/v1/data/evm/{chainId}/address/{address}/internal-txs",
	"/api/v1/data/evm/{chainId}/address/{address}/balances",
	"/api/v1/data/evm/{chainId}/address/{address}/native",
	"/api/v1/data/evm/{chainId}/stablecoins",
	"/api/v1/data/evm/{chainId}/stablecoins/timeseries",
	"/api/v1/data/pchain/txs",
	"/api/v1/data/pchain/txs/{txId}",
	"/api/v1/data/pchain/tx-types",
	"/api/v1/data/pchain/stats",
	"/api/v1/data/pchain/subnet-timeline",
	"/api/v1/data/pchain/blocks",
	"/api/v1/data/pchain/blocks/{number}",
	"/api/v1/data/subnets/{subnetId}",
	"/api/v1/data/chains",
	"/api/v1/data/chains/{chainId}/risk",
	"/api/v1/data/validators",
	"/api/v1/data/validators/{id}",
	"/api/v1/data/validators/{id}/deposits",
	"/api/v1/data/genesis",
	"/api/v1/data/genesis/allocations",
	"/api/v1/metrics/fees",
	"/api/v1/metrics/fees/daily",
	"/api/v1/metrics/evm/{chainId}/fees/burned",
	"/api/v1/metrics/burned/total",
	"/api/v1/metrics/evm/{chainId}/stats",
	"/api/v1/metrics/evm/{chainId}/timeseries",
	"/api/v1/metrics/evm/{chainId}/timeseries/{metric}",
	"/api/v1/metrics/staking",
	"/api/v1/metrics/staking/unlocks",
	"/api/v1/metrics/staking/liquid",
	"/api/v1/metrics/restaking",
	"/api/v1/metrics/lending",
	"/api/v1/metrics/dex",
	"/api/v1/metrics/dex/pools",
	"/api/v1/metrics/dex/timeseries",
	"/api/v1/metrics/bridges",
	"/api/v1/metrics/perps",
	"/api/v1/metrics/icm",
	"/api/v1/metrics/icm/apps",
	"/api/v1/metrics/icm/timeseries",
	"/api/v1/metrics/icm/messages",
	"/api/v1/metrics/icm/transfers/top",
	"/api/v1/metrics/icm/value",
	"/api/v1/metrics/icm/value/timeseries",
}

const apiDescription = `Avalanche data from L1Beat's indexer: C-Chain blocks, transactions, balances and DeFi metrics, P-Chain transactions, L1s and validators, staking, and the network genesis.

**Scope.** EVM, DeFi and ICM data cover the Avalanche C-Chain (chain ID 43114) only. ` + "`/api/v1/treasuries`" + ` is served on this host by the L1Beat backend and is not part of this spec. Live blocks stream over WebSocket at ` + "`wss://api.l1beat.io/ws/blocks/43114`" + `, which OpenAPI cannot describe.

**Units.**
- Values that can exceed 2^53 are decimal strings: token amounts in base units (divide by 10^decimals), P-Chain stake and fees in nAVAX (1 AVAX = 10^9 nAVAX), and C-Chain native amounts in wei (10^18).
- USD values are JSON numbers.
- Fields ending in ` + "`_percent`" + ` are percent (0-100); ` + "`priced_share`" + ` and similar shares are ratios (0-1).
- Timestamps are RFC 3339 in UTC. Addresses are lowercase 0x hex; P-Chain IDs are CB58.
- Fields marked nullable can be ` + "`null`" + ` (for example ` + "`created_time`" + ` of a chain whose creation was never indexed, or ` + "`block_time`" + ` of C-Chain block 0, whose header timestamp is 0).

**Windows.** Metrics with a ` + "`days`" + ` parameter cover N complete UTC days ending at ` + "`as_of`" + ` (normally yesterday), not a rolling 24 hours. A window that reaches back before the start of the data is clamped, so the returned ` + "`days`" + ` can be smaller than requested.

**Pagination.** List endpoints take ` + "`limit`" + ` and ` + "`offset`" + `; where a response carries ` + "`meta.next_cursor`" + `, pass it back as ` + "`cursor`" + ` (or ` + "`before`" + ` on ICM messages) for stable paging. Treat cursors as opaque.

**Errors.** Errors return ` + "`{\"error\":{\"code\",\"message\"}}`" + ` with code INVALID_PARAMETER (400), NOT_FOUND (404), RATE_LIMITED (429) or INTERNAL_ERROR (500).

**Rate limits.** 180 requests per minute per IP with a burst of 30 (token bucket). Every response carries X-RateLimit-Limit (burst size), X-RateLimit-Remaining and X-RateLimit-Reset (epoch second when the bucket is full again); a 429 also carries Retry-After.

**Caching.** Metric and data responses are cached for 1-10 minutes and sent with Cache-Control: public, max-age=60, stale-while-revalidate=300.`

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: openapi <swagger.json> <pkg/api dir> <out.json>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(swaggerPath, apiDir, outPath string) error {
	raw, err := os.ReadFile(swaggerPath)
	if err != nil {
		return err
	}
	var v2 openapi2.T
	if err := json.Unmarshal(raw, &v2); err != nil {
		return fmt.Errorf("parse swagger: %w", err)
	}

	// Keep only the public endpoints before converting.
	keep := map[string]bool{}
	for _, p := range publicPaths {
		keep[p] = true
	}
	for p := range v2.Paths {
		if !keep[p] {
			delete(v2.Paths, p)
		}
	}
	for _, p := range publicPaths {
		if _, ok := v2.Paths[p]; !ok {
			return fmt.Errorf("public path %s has no swag annotation", p)
		}
	}

	doc, err := openapi2conv.ToV3(&v2)
	if err != nil {
		return fmt.Errorf("convert to OpenAPI 3: %w", err)
	}

	doc.Info.Title = "L1Beat API"
	doc.Info.Description = apiDescription
	doc.Servers = openapi3.Servers{{URL: "https://api.l1beat.io"}}

	nullable, err := pointerFields(apiDir)
	if err != nil {
		return err
	}
	for name, ref := range doc.Components.Schemas {
		fields := nullable[strings.TrimPrefix(name, "api.")]
		for prop, s := range ref.Value.Properties {
			if !fields[prop] {
				continue
			}
			if s.Ref != "" {
				// A $ref cannot carry nullable in 3.0: wrap it.
				ref.Value.Properties[prop] = &openapi3.SchemaRef{Value: &openapi3.Schema{
					AllOf: openapi3.SchemaRefs{{Ref: s.Ref}}, Nullable: true,
				}}
				continue
			}
			s.Value.Nullable = true
		}
	}

	addSharedResponses(doc)
	pruneSchemas(doc)

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, append(out, '\n'), 0o644)
}

// pointerFields maps each struct in the api package to the JSON names of its
// pointer fields, which the handlers set to nil to send null.
func pointerFields(dir string) (map[string]map[string]bool, error) {
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			return nil, err
		}
		{
			ast.Inspect(f, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, field := range st.Fields.List {
					if _, isPtr := field.Type.(*ast.StarExpr); !isPtr || field.Tag == nil {
						continue
					}
					tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("json")
					name := strings.Split(tag, ",")[0]
					if name == "" || name == "-" {
						continue
					}
					if out[ts.Name.Name] == nil {
						out[ts.Name.Name] = map[string]bool{}
					}
					out[ts.Name.Name][name] = true
				}
				return true
			})
		}
	}
	return out, nil
}

func addSharedResponses(doc *openapi3.T) {
	intHeader := func(desc string) *openapi3.HeaderRef {
		return &openapi3.HeaderRef{Value: &openapi3.Header{Parameter: openapi3.Parameter{
			Description: desc,
			Schema:      &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()},
		}}}
	}
	doc.Components.Headers = openapi3.Headers{
		"X-RateLimit-Limit":     intHeader("Burst size of the per-IP token bucket"),
		"X-RateLimit-Remaining": intHeader("Requests left in the bucket"),
		"X-RateLimit-Reset":     intHeader("Epoch second when the bucket is full again"),
		"Retry-After":           intHeader("Seconds to wait before retrying"),
	}
	rateHeaders := openapi3.Headers{
		"X-RateLimit-Limit":     {Ref: "#/components/headers/X-RateLimit-Limit"},
		"X-RateLimit-Remaining": {Ref: "#/components/headers/X-RateLimit-Remaining"},
		"X-RateLimit-Reset":     {Ref: "#/components/headers/X-RateLimit-Reset"},
	}

	if s, ok := doc.Components.Schemas["api.ErrorCode"]; ok {
		s.Value.Description = "INVALID_PARAMETER (400), NOT_FOUND (404), RATE_LIMITED (429), INTERNAL_ERROR (500)"
	}
	errContent := openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Ref: "#/components/schemas/api.ErrorResponse"})
	tooMany := &openapi3.ResponseRef{Value: &openapi3.Response{
		Description: ptr("Rate limit exceeded"),
		Headers: openapi3.Headers{
			"Retry-After":           {Ref: "#/components/headers/Retry-After"},
			"X-RateLimit-Limit":     {Ref: "#/components/headers/X-RateLimit-Limit"},
			"X-RateLimit-Remaining": {Ref: "#/components/headers/X-RateLimit-Remaining"},
			"X-RateLimit-Reset":     {Ref: "#/components/headers/X-RateLimit-Reset"},
		},
		Content: errContent,
	}}

	paths := doc.Paths.InMatchingOrder()
	sort.Strings(paths)
	for _, p := range paths {
		for _, op := range doc.Paths.Value(p).Operations() {
			for code, r := range op.Responses.Map() {
				if r.Value == nil {
					continue
				}
				if r.Value.Headers == nil {
					r.Value.Headers = openapi3.Headers{}
				}
				for k, h := range rateHeaders {
					r.Value.Headers[k] = h
				}
				if code != "200" && r.Value.Content == nil {
					r.Value.Content = errContent
				}
			}
			if p != "/health" {
				op.Responses.Set("429", tooMany)
			}
		}
	}
}

// pruneSchemas drops component schemas that no public endpoint references.
func pruneSchemas(doc *openapi3.T) {
	raw, _ := json.Marshal(doc.Paths)
	used := map[string]bool{}
	var queue []string
	mark := func(b []byte) {
		for _, part := range strings.Split(string(b), `"#/components/schemas/`)[1:] {
			name := part[:strings.Index(part, `"`)]
			if !used[name] {
				used[name] = true
				queue = append(queue, name)
			}
		}
	}
	mark(raw)
	mark([]byte(`"#/components/schemas/api.ErrorResponse"`))
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if s, ok := doc.Components.Schemas[name]; ok {
			b, _ := json.Marshal(s.Value)
			mark(b)
		}
	}
	for name := range doc.Components.Schemas {
		if !used[name] {
			delete(doc.Components.Schemas, name)
		}
	}
}

func ptr[T any](v T) *T { return &v }
