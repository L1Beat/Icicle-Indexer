package cmd

import (
	"context"
	"log"

	"icicle/pkg/chwrapper"
	"icicle/pkg/pchainsyncer"
)

// RunLendingRebackfill re-reads one protocol's past lending_market_snapshots
// days from the archive node and overwrites them. Uses the node of the P-Chain
// entry in config.yaml, like the indexer's live snapshots.
func RunLendingRebackfill(ctx context.Context, protocol string) {
	configs, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	var nodeURL string
	for _, c := range configs {
		if c.VM == "p" {
			nodeURL = c.RpcURL
			break
		}
	}
	if nodeURL == "" {
		log.Fatal("No P-Chain (vm: p) entry found in config.yaml")
	}
	conn, err := chwrapper.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()
	if err := pchainsyncer.RebackfillLendingProtocol(ctx, conn, nodeURL, protocol); err != nil {
		log.Fatalf("Lending rebackfill failed: %v", err)
	}
}
