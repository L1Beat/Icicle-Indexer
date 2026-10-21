package enrich

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed curated/*.json
var curatedFS embed.FS

type curatedFile struct {
	ChainID uint32 `json:"chain_id"`
	Labels  []struct {
		Address        string `json:"address"`
		Name           string `json:"name"`
		Kind           string `json:"kind"`
		Protocol       string `json:"protocol"`
		Verified       bool   `json:"verified"`
		Implementation string `json:"implementation"`
	} `json:"labels"`
}

// CuratedLabels returns the bundled curated labels grouped by chain. Every
// entry is a contract (we do not curate wallets) and carries Source=curated.
func CuratedLabels() (map[uint32][]Label, error) {
	out := make(map[uint32][]Label)
	err := fs.WalkDir(curatedFS, "curated", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		data, err := curatedFS.ReadFile(path)
		if err != nil {
			return err
		}
		var f curatedFile
		if err := json.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for _, e := range f.Labels {
			addr, ok := NormalizeAddress(e.Address)
			if !ok {
				return fmt.Errorf("%s: bad address %q", path, e.Address)
			}
			if e.Name == "" {
				return fmt.Errorf("%s: %s has no name", path, addr)
			}
			if !validKind(e.Kind) {
				return fmt.Errorf("%s: %s has unknown kind %q", path, addr, e.Kind)
			}
			impl := ""
			if e.Implementation != "" {
				impl, ok = NormalizeAddress(e.Implementation)
				if !ok {
					return fmt.Errorf("%s: %s bad implementation %q", path, addr, e.Implementation)
				}
			}
			out[f.ChainID] = append(out[f.ChainID], Label{
				Address:        addr,
				Name:           e.Name,
				Kind:           e.Kind,
				Protocol:       e.Protocol,
				IsContract:     true,
				Verified:       e.Verified,
				Implementation: impl,
				Source:         SourceCurated,
			})
		}
		return nil
	})
	return out, err
}

func validKind(k string) bool {
	for _, known := range Kinds {
		if k == known {
			return true
		}
	}
	return false
}

// NormalizeAddress lowercases and 0x-prefixes a 20-byte hex address.
func NormalizeAddress(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 40 {
		return "", false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return "0x" + s, true
}
