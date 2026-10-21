package enrich

import (
	_ "embed"
	"strings"
)

//go:embed signatures_seed.txt
var signaturesSeed string

// SeedSignatures returns the bundled signature list with computed selectors.
func SeedSignatures() []SignatureRow {
	var rows []SignatureRow
	for _, line := range strings.Split(signaturesSeed, "\n") {
		sig := strings.TrimSpace(line)
		if sig == "" || strings.HasPrefix(sig, "#") {
			continue
		}
		rows = append(rows, SignatureRow{
			Selector:  SelectorOf(sig),
			Signature: sig,
			Name:      FunctionName(sig),
			Source:    "seed",
			Priority:  PrioritySeed,
		})
	}
	return rows
}

// SignatureRow mirrors one row of the function_signatures table.
type SignatureRow struct {
	Selector  [4]byte
	Signature string // "" means looked up and unknown (negative cache)
	Name      string
	Source    string
	Priority  uint8
}
