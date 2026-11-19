package pchainsyncer

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"icicle/pkg/chwrapper"
	"icicle/pkg/lendingmarkets"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const cChainID = 43114

var (
	selAsset           = []byte{0x38, 0xd5, 0x2e, 0x0f} // asset()
	selConvertToAssets = []byte{0x07, 0xa2, 0xd1, 0x3a} // convertToAssets(uint256)
)

// registryToken is a stablecoin-registry token with its decimals.
type registryToken struct {
	token    []byte // 20 bytes
	decimals uint8
}

// SnapshotFundPrices writes tokenized_fund_prices rows for the floating-NAV
// tokenized funds that price themselves on-chain: ERC-4626 vaults whose
// underlying asset is a registry USD stablecoin, priced at convertToAssets of
// one whole share. Funds that are not such vaults get no row, and a fund whose
// read fails keeps its previous row.
func SnapshotFundPrices(ctx context.Context, conn clickhouse.Conn, caller lendingmarkets.Caller) error {
	funds, err := queryRegistry(ctx, conn, "category = 'tokenized_fund' AND peg = 'NAV'")
	if err != nil {
		return err
	}
	if len(funds) == 0 {
		return nil
	}
	usdList, err := queryRegistry(ctx, conn, "category = 'stablecoin' AND peg = 'USD'")
	if err != nil {
		return err
	}
	usd := make(map[string]uint8, len(usdList))
	for _, t := range usdList {
		usd[string(t.token)] = t.decimals
	}

	var tuples []string
	var args []interface{}
	var errs []error
	now := time.Now().UTC()
	for _, f := range funds {
		price, ok, err := readVaultPrice(ctx, caller, f, usd)
		if err != nil {
			errs = append(errs, fmt.Errorf("fund 0x%x: %w", f.token, err))
			continue
		}
		if !ok {
			continue
		}
		tuples = append(tuples, "(?, ?, ?, ?, ?)")
		args = append(args, uint32(cChainID), string(f.token), price, "erc4626", now)
	}
	if len(tuples) > 0 {
		wctx, cancel := chwrapper.WriteContext(ctx)
		defer cancel()
		if err := conn.Exec(wctx, `
			INSERT INTO tokenized_fund_prices (chain_id, token, price_usd, source, sampled_at)
			VALUES `+strings.Join(tuples, ", "), args...); err != nil {
			return fmt.Errorf("failed to insert fund prices: %w", err)
		}
	}
	return errors.Join(errs...)
}

func queryRegistry(ctx context.Context, conn clickhouse.Conn, where string) ([]registryToken, error) {
	rows, err := conn.Query(ctx, `SELECT token, decimals FROM stablecoins FINAL WHERE chain_id = ? AND `+where, uint32(cChainID))
	if err != nil {
		return nil, fmt.Errorf("failed to read stablecoin registry: %w", err)
	}
	defer rows.Close()
	var out []registryToken
	for rows.Next() {
		var t registryToken
		var token string
		if err := rows.Scan(&token, &t.decimals); err != nil {
			return nil, err
		}
		t.token = []byte(token)
		out = append(out, t)
	}
	return out, rows.Err()
}

// readVaultPrice returns the USD price of one whole share of an ERC-4626 vault
// over a USD stablecoin in usd (token bytes -> decimals). ok is false, with no
// error, when the fund is not such a vault: asset() reverts or is not a USD
// stablecoin.
func readVaultPrice(ctx context.Context, c lendingmarkets.Caller, f registryToken, usd map[string]uint8) (float64, bool, error) {
	to := "0x" + hex.EncodeToString(f.token)
	out, err := c.Call(ctx, to, selAsset)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "revert") {
			return 0, false, nil
		}
		return 0, false, err
	}
	if len(out) != 32 {
		return 0, false, nil
	}
	assetDec, isUSD := usd[string(out[12:32])]
	if !isUSD {
		return 0, false, nil
	}

	oneShare := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(f.decimals)), nil)
	arg := make([]byte, 32)
	oneShare.FillBytes(arg)
	out, err = c.Call(ctx, to, append(append([]byte{}, selConvertToAssets...), arg...))
	if err != nil {
		return 0, false, err
	}
	if len(out) != 32 {
		return 0, false, fmt.Errorf("convertToAssets returned %d bytes", len(out))
	}
	assets := new(big.Int).SetBytes(out)
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(assetDec)), nil)
	price, _ := new(big.Float).Quo(new(big.Float).SetInt(assets), new(big.Float).SetInt(unit)).Float64()
	// A share worth nothing or a million dollars is a broken read, not a NAV.
	if price <= 0 || price > 1e6 {
		return 0, false, fmt.Errorf("implausible share price %v", price)
	}
	return price, true, nil
}
