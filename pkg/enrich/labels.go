package enrich

import (
	"sort"
	"strings"
)

// Label sources, in winning order: curated beats verified-source beats
// token-metadata. "unverified" rows are negative-cache entries written by the
// Routescan fetcher so an address is not re-queried; they never surface.
const (
	SourceCurated        = "curated"
	SourceVerifiedSource = "verified-source"
	SourceTokenMetadata  = "token-metadata"
	SourceUnverified     = "unverified"
)

// Label kinds accepted by the API.
var Kinds = []string{"protocol", "token", "pool", "router", "bridge", "exchange", "wallet", "contract"}

// Label is one labelling of an address from one source.
type Label struct {
	Address        string // 0x-prefixed lowercase hex
	Name           string
	Kind           string
	Protocol       string
	IsContract     bool
	Verified       bool
	Implementation string // 0x-prefixed lowercase hex, "" if not a proxy
	Source         string
}

func sourceRank(source string) int {
	switch source {
	case SourceCurated:
		return 0
	case SourceVerifiedSource:
		return 1
	case SourceTokenMetadata:
		return 2
	default:
		return 3
	}
}

// MergeLabels folds the labels one address received from several sources into
// the single label the API exposes. The highest-ranked source with a name
// provides every field it has; empty fields are filled from lower-ranked
// sources (so a curated name still inherits the implementation address found
// by the source lookup, and verified stays true if any source verified it).
// A verified-source label that falls back to the generic "contract" kind is
// promoted to "token" when token metadata exists for the address. Returns nil
// when no source has a name.
func MergeLabels(labels []Label) *Label {
	named := make([]Label, 0, len(labels))
	for _, l := range labels {
		if l.Name != "" && l.Source != SourceUnverified {
			named = append(named, l)
		}
	}
	if len(named) == 0 {
		return nil
	}
	sort.SliceStable(named, func(i, j int) bool {
		return sourceRank(named[i].Source) < sourceRank(named[j].Source)
	})
	out := named[0]
	hasToken := false
	for _, l := range named {
		if l.Source == SourceTokenMetadata {
			hasToken = true
		}
	}
	for _, l := range named[1:] {
		if out.Kind == "" {
			out.Kind = l.Kind
		}
		if out.Protocol == "" {
			out.Protocol = l.Protocol
		}
		if out.Implementation == "" {
			out.Implementation = l.Implementation
		}
		out.IsContract = out.IsContract || l.IsContract
		out.Verified = out.Verified || l.Verified
	}
	// Any address with a label from the verified-source or token-metadata path
	// is a contract even if the curated entry forgot to say so.
	for _, l := range labels {
		if l.Source == SourceVerifiedSource || l.Source == SourceTokenMetadata {
			out.IsContract = true
		}
	}
	if out.Kind == "" || (out.Kind == "contract" && hasToken) {
		if hasToken {
			out.Kind = "token"
		} else {
			out.Kind = "contract"
		}
	}
	return &out
}

// KindFromContractName guesses a label kind from a verified contract's name.
// It is deliberately conservative: anything unrecognised is "contract".
func KindFromContractName(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "router") || strings.Contains(n, "aggregat") || strings.Contains(n, "swapper") || strings.Contains(n, "augustus"):
		return "router"
	case strings.Contains(n, "bridge") || strings.Contains(n, "tokenmessenger") || strings.Contains(n, "endpoint") || strings.Contains(n, "teleporter"):
		return "bridge"
	case strings.Contains(n, "pair") || strings.Contains(n, "pool") || strings.Contains(n, "lbpair") || strings.Contains(n, "vault"):
		return "pool"
	case strings.Contains(n, "token") || strings.Contains(n, "erc20") || strings.Contains(n, "wavax") || strings.Contains(n, "fiat"):
		return "token"
	default:
		return "contract"
	}
}

// IsProxyName reports whether a verified contract name is a generic proxy
// wrapper, in which case the implementation's name is more descriptive.
func IsProxyName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "proxy") || n == "unitroller" || strings.HasSuffix(n, "delegator")
}
