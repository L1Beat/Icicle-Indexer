package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"icicle/pkg/enrich"
)

// Transaction represents an EVM transaction
type Transaction struct {
	ChainID          uint32    `json:"chain_id" example:"43114"`
	Hash             string    `json:"hash" example:"0x1234..."`
	BlockNumber      uint32    `json:"block_number" example:"12345678"`
	BlockTime        time.Time `json:"block_time"`
	TransactionIndex uint16    `json:"transaction_index" example:"0"`
	From             string    `json:"from" example:"0x742d35Cc6634C0532925a3b844Bc9e7595f..."`
	To               *string   `json:"to" example:"0x123..."` // null for contract creation
	Value            string    `json:"value" example:"1000000000000000000"`
	GasLimit         uint32    `json:"gas_limit" example:"21000"`
	GasPrice         uint64    `json:"gas_price" example:"25000000000"`
	GasUsed          uint32    `json:"gas_used" example:"21000"`
	Success          bool      `json:"success" example:"true"`
	Type             uint8     `json:"type" example:"2"`

	// Enrichment (additive, 2026-10). All null when unknown.
	Method    *enrich.Method `json:"method"`     // resolved 4-byte selector; null for plain transfers / unknown selectors
	FromLabel *AddressLabel  `json:"from_label"` // label of the sender, if known
	ToLabel   *AddressLabel  `json:"to_label"`   // label of the recipient, if known
	Intent    *enrich.Intent `json:"intent"`     // coarse server-side reading of the tx; null when nothing can be said
}

// InternalTransaction represents an internal transaction (trace)
type InternalTransaction struct {
	TxHash      string    `json:"tx_hash,omitempty" example:"0x1234..."`
	BlockNumber uint32    `json:"block_number,omitempty" example:"12345678"`
	BlockTime   time.Time `json:"block_time,omitempty"`
	TraceIndex  string    `json:"trace_index" example:"0,1,2"` // Path in call tree
	From        string    `json:"from" example:"0x742d35Cc..."`
	To          *string   `json:"to" example:"0x123..."` // null for CREATE
	Value       string    `json:"value" example:"1000000000000000000"`
	GasUsed     uint32    `json:"gas_used" example:"21000"`
	CallType    string    `json:"call_type" example:"CALL"`
	Success     bool      `json:"success" example:"true"`
}

// TokenTransfer represents an ERC-20 token transfer event
type TokenTransfer struct {
	Token      string        `json:"token" example:"0xb97ef9ef..."`
	Name       *string       `json:"name,omitempty" example:"USD Coin"`
	Symbol     *string       `json:"symbol,omitempty" example:"USDC"`
	Decimals   *uint8        `json:"decimals,omitempty" example:"6"`
	TokenLabel *AddressLabel `json:"token_label"` // label of the token contract, if known
	From       string        `json:"from" example:"0x742d35Cc..."`
	To         string        `json:"to" example:"0x123..."`
	Value      string        `json:"value" example:"1000000"`
	LogIndex   uint32        `json:"log_index" example:"5"`
}

// TokenApproval represents an ERC-20 approval event
type TokenApproval struct {
	Token       string        `json:"token" example:"0xb97ef9ef..."`
	Name        *string       `json:"name,omitempty" example:"USD Coin"`
	Symbol      *string       `json:"symbol,omitempty" example:"USDC"`
	Decimals    *uint8        `json:"decimals,omitempty" example:"6"`
	TokenLabel  *AddressLabel `json:"token_label"` // label of the token contract, if known
	Owner       string        `json:"owner" example:"0x742d35Cc..."`
	Spender     string        `json:"spender" example:"0x123..."`
	Amount      string        `json:"amount" example:"1000000"`
	IsUnlimited bool          `json:"is_unlimited" example:"false"`
	LogIndex    uint32        `json:"log_index" example:"5"`
}

// TransactionDetail represents a transaction with its internal transactions and token transfers
type TransactionDetail struct {
	Transaction
	InternalTxs    []InternalTransaction `json:"internal_txs"`
	TokenTransfers []TokenTransfer       `json:"token_transfers"`
	Approvals      []TokenApproval       `json:"approvals"`
}

// txSelectCols is the column list every transaction query selects. The last
// two columns (selector, input_len) feed method resolution; the full calldata
// is never shipped to the client.
const txSelectCols = `chain_id, hash, block_number, block_time, transaction_index,
				from, to, value, gas_limit, gas_price, gas_used, success, type,
				substring(input, 1, 4) AS selector, toUInt64(length(input)) AS input_len`

// Caps applied when list endpoints embed transfers (include=transfers).
const (
	includeTransfersMaxLimit = 50    // page size cap with include=transfers
	maxInternalTxsPerTx      = 100   // internal txs kept per transaction
	maxTokenEventsPerTx      = 200   // token transfers kept per transaction
	maxApprovalsPerTx        = 50    // approvals kept per transaction
	maxEmbeddedRows          = 10000 // hard cap on rows read per batch query
)

// scanner is the subset of driver.Rows / driver.Row the tx scanner needs.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanTx reads one txSelectCols row and resolves method + labels.
func (s *Server) scanTx(sc scanner) (Transaction, uint64, error) {
	var tx Transaction
	var hashBytes [32]byte
	var fromBytes [20]byte
	var toBytes []byte
	var valueBig big.Int
	var selector []byte
	var inputLen uint64

	if err := sc.Scan(
		&tx.ChainID, &hashBytes, &tx.BlockNumber, &tx.BlockTime, &tx.TransactionIndex,
		&fromBytes, &toBytes, &valueBig, &tx.GasLimit, &tx.GasPrice, &tx.GasUsed, &tx.Success, &tx.Type,
		&selector, &inputLen,
	); err != nil {
		return tx, 0, err
	}

	tx.Hash = "0x" + hex.EncodeToString(hashBytes[:])
	tx.From = "0x" + hex.EncodeToString(fromBytes[:])
	if len(toBytes) > 0 {
		to := "0x" + hex.EncodeToString(toBytes)
		tx.To = &to
	}
	tx.Value = valueBig.String()

	tx.Method = s.methodFor(selector, inputLen)
	tx.FromLabel = s.labelFor(tx.ChainID, tx.From)
	if tx.To != nil {
		tx.ToLabel = s.labelFor(tx.ChainID, *tx.To)
	}
	return tx, inputLen, nil
}

// wantsTransfers reports whether ?include= lists "transfers".
func wantsTransfers(r *http.Request) bool {
	for _, part := range strings.Split(r.URL.Query().Get("include"), ",") {
		if strings.TrimSpace(part) == "transfers" {
			return true
		}
	}
	return false
}

// handleListTxs returns a paginated list of transactions
// @Summary List transactions
// @Description Get a paginated list of transactions for a specific chain
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param limit query int false "Number of results (max 100; 50 with include=transfers)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Param include query string false "Set to transfers to embed token_transfers, internal_txs and approvals on every row"
// @Success 200 {object} Response{data=[]Transaction,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/txs [get]
func (s *Server) handleListTxs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)
	includeTransfers := wantsTransfers(r)
	if includeTransfers && limit > includeTransfersMaxLimit {
		limit = includeTransfersMaxLimit
	}

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	fetchLimit := limit + 1

	var query string
	var args []interface{}
	// Optional exact-block filter: list a single block's transactions in order.
	// block_number is a sort-key column, so this is an indexed range read.
	if bn := r.URL.Query().Get("block_number"); bn != "" {
		if parsed, perr := strconv.ParseUint(bn, 10, 64); perr == nil {
			query = `
				SELECT ` + txSelectCols + `
				FROM raw_txs
				WHERE chain_id = ? AND block_number = ?
				ORDER BY transaction_index ASC
				LIMIT ? OFFSET ?
			`
			args = []interface{}{chainID, parsed, fetchLimit, offset}
		}
	}
	if query == "" && cursor != nil && cursor.HasTxIndex {
		query = `
			SELECT ` + txSelectCols + `
			FROM raw_txs
			WHERE chain_id = ? AND (block_number, transaction_index) < (?, ?)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ?
		`
		args = []interface{}{chainID, cursor.BlockNumber, cursor.TxIndex, fetchLimit}
	} else if query == "" {
		query = `
			SELECT ` + txSelectCols + `
			FROM raw_txs
			WHERE chain_id = ?
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{chainID, fetchLimit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	txs := []Transaction{}
	inputLens := []uint64{}
	for rows.Next() {
		tx, inputLen, err := s.scanTx(rows)
		if err != nil {
			writeInternalError(w, err.Error())
			return
		}
		txs = append(txs, tx)
		inputLens = append(inputLens, inputLen)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	txs, hasMore := trimResults(txs, limit)
	inputLens = inputLens[:len(txs)]

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(txs) > 0 {
		last := txs[len(txs)-1]
		meta.NextCursor = cursorBlockTx(last.BlockNumber, last.TransactionIndex)
	}

	if wantCount {
		var total int64
		_ = s.conn.QueryRow(ctx, `SELECT toInt64(count()) FROM raw_txs WHERE chain_id = ?`, chainID).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: s.finishTxList(ctx, chainID, txs, inputLens, includeTransfers, ""),
		Meta: meta,
	})
}

// finishTxList attaches intents (and, when requested, embedded transfers) to a
// page of transactions. viewer, when set, is the address whose page this is:
// a plain "send" aimed at the viewer is reported as "receive".
func (s *Server) finishTxList(ctx context.Context, chainID uint32, txs []Transaction, inputLens []uint64, includeTransfers bool, viewer string) interface{} {
	if !includeTransfers {
		for i := range txs {
			txs[i].Intent = s.intentFor(&txs[i], nil, inputLens[i] > 0, viewer)
		}
		return txs
	}
	details := s.attachTransfers(ctx, chainID, txs)
	for i := range details {
		details[i].Intent = s.intentFor(&details[i].Transaction, &details[i], inputLens[i] > 0, viewer)
	}
	return details
}

// intentFor builds the rule input from what we know about the tx and applies
// the intent rules. detail is nil when transfers were not loaded.
func (s *Server) intentFor(tx *Transaction, detail *TransactionDetail, hasCalldata bool, viewer string) *enrich.Intent {
	in := enrich.IntentInput{
		Method:      tx.Method,
		HasCalldata: hasCalldata,
		ValueOut:    tx.Value != "" && tx.Value != "0",
		Sender:      tx.From,
	}
	if tx.To != nil {
		in.To = *tx.To
		in.ToLabel = s.rawLabelFor(tx.ChainID, *tx.To)
	}
	if detail != nil {
		in.HasTransfer = true
		in.Transfers = len(detail.TokenTransfers)
		in.Approvals = len(detail.Approvals)
		sender := strings.ToLower(tx.From)
		for _, tt := range detail.TokenTransfers {
			if strings.ToLower(tt.From) == sender {
				in.TokensOut++
			}
			if strings.ToLower(tt.To) == sender {
				in.TokensIn++
			}
		}
		for _, itx := range detail.InternalTxs {
			if itx.To != nil && strings.ToLower(*itx.To) == sender {
				in.NativeIn = true
			}
		}
	}
	intent := enrich.DeriveIntent(in)
	if intent != nil && intent.Kind == "send" && viewer != "" && tx.To != nil &&
		strings.EqualFold(*tx.To, viewer) && !strings.EqualFold(tx.From, viewer) {
		intent.Kind = "receive"
		intent.Label = strings.Replace(intent.Label, "Sent", "Received", 1)
	}
	return intent
}

// attachTransfers loads internal txs, token transfers and approvals for a page
// of transactions with three batched queries constrained by the page's block
// numbers (primary-key reads), never one query per transaction. Failures are
// logged and leave the lists empty, matching the single-tx endpoint.
func (s *Server) attachTransfers(ctx context.Context, chainID uint32, txs []Transaction) []TransactionDetail {
	details := make([]TransactionDetail, len(txs))
	index := make(map[string]int, len(txs))
	for i, tx := range txs {
		details[i] = TransactionDetail{
			Transaction:    tx,
			InternalTxs:    []InternalTransaction{},
			TokenTransfers: []TokenTransfer{},
			Approvals:      []TokenApproval{},
		}
		index[tx.Hash] = i
	}
	if len(txs) == 0 {
		return details
	}

	hashList, blockList, minTime, maxTime := txBatchKeys(txs)

	// Internal transactions (traces with value > 0, excluding top-level)
	traceRows, err := s.conn.Query(ctx, `
		SELECT tx_hash, trace_address, from, to, value, gas_used, call_type, tx_success
		FROM raw_traces
		WHERE chain_id = ? AND block_number IN (`+blockList+`) AND tx_hash IN (`+hashList+`)
		  AND value > 0
		  AND length(trace_address) > 0
		ORDER BY tx_hash, trace_address
		LIMIT `+strconv.Itoa(maxEmbeddedRows), chainID)
	if err != nil {
		slog.Error("error querying internal transactions", "error", err)
	} else {
		defer traceRows.Close()
		for traceRows.Next() {
			var itx InternalTransaction
			var txHash [32]byte
			var fromBytes [20]byte
			var toBytes []byte
			var valueBig big.Int
			var traceAddr []uint16
			if err := traceRows.Scan(&txHash, &traceAddr, &fromBytes, &toBytes, &valueBig, &itx.GasUsed, &itx.CallType, &itx.Success); err != nil {
				slog.Error("error scanning internal transaction", "error", err)
				continue
			}
			i, ok := index["0x"+hex.EncodeToString(txHash[:])]
			if !ok || len(details[i].InternalTxs) >= maxInternalTxsPerTx {
				continue
			}
			itx.From = "0x" + hex.EncodeToString(fromBytes[:])
			if len(toBytes) > 0 {
				to := "0x" + hex.EncodeToString(toBytes)
				itx.To = &to
			}
			itx.Value = valueBig.String()
			traceStrs := make([]string, len(traceAddr))
			for j, v := range traceAddr {
				traceStrs[j] = fmt.Sprintf("%d", v)
			}
			itx.TraceIndex = strings.Join(traceStrs, ",")
			details[i].InternalTxs = append(details[i].InternalTxs, itx)
		}
		if err := traceRows.Err(); err != nil {
			slog.Error("error iterating internal transactions", "error", err)
		}
	}

	// ERC-20 Transfer and Approval events in one pass.
	// Transfer(address indexed from, address indexed to, uint256 value)
	// Approval(address indexed owner, address indexed spender, uint256 value)
	// raw_logs is ordered by (chain_id, block_time, ...) so the block_time range
	// is the primary-key read; block_number IN uses the minmax skip index.
	logRows, err := s.conn.Query(ctx, `
		SELECT
			l.transaction_hash,
			l.topic0,
			l.address,
			COALESCE(tm.name, '') as name,
			COALESCE(tm.symbol, '') as symbol,
			COALESCE(tm.decimals, 0) as decimals,
			substring(l.topic1, 13, 20) as addr1,
			substring(l.topic2, 13, 20) as addr2,
			reinterpretAsUInt256(reverse(substring(l.data, 1, 32))) as value,
			l.log_index
		FROM raw_logs l
		LEFT JOIN token_metadata tm FINAL ON l.chain_id = tm.chain_id AND l.address = tm.token
		WHERE l.chain_id = ?
		  AND l.block_time BETWEEN ? AND ?
		  AND l.block_number IN (`+blockList+`)
		  AND l.transaction_hash IN (`+hashList+`)
		  AND l.topic0 IN (unhex('ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef'),
		                   unhex('8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925'))
		  AND length(l.data) >= 32
		  AND l.topic1 IS NOT NULL
		  AND l.topic2 IS NOT NULL
		ORDER BY l.transaction_hash, l.log_index
		LIMIT `+strconv.Itoa(maxEmbeddedRows), chainID, minTime, maxTime)
	if err != nil {
		slog.Error("error querying token events", "error", err)
		return details
	}
	defer logRows.Close()

	// MAX_UINT256 = 2^256 - 1 (indicates unlimited approval)
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	for logRows.Next() {
		var txHash, topic0 [32]byte
		var tokenBytes [20]byte
		var addr1, addr2 []byte
		var name, symbol string
		var decimals uint8
		var valueBig big.Int
		var logIndex uint32
		if err := logRows.Scan(&txHash, &topic0, &tokenBytes, &name, &symbol, &decimals, &addr1, &addr2, &valueBig, &logIndex); err != nil {
			slog.Error("error scanning token event", "error", err)
			continue
		}
		i, ok := index["0x"+hex.EncodeToString(txHash[:])]
		if !ok {
			continue
		}
		token := "0x" + hex.EncodeToString(tokenBytes[:])
		var namePtr, symbolPtr *string
		var decimalsPtr *uint8
		if name != "" {
			namePtr = &name
		}
		if symbol != "" {
			symbolPtr = &symbol
		}
		if decimals > 0 || name != "" || symbol != "" {
			decimalsPtr = &decimals
		}
		tokenLabel := s.labelFor(chainID, token)

		if topic0[0] == 0xdd { // Transfer
			if len(details[i].TokenTransfers) >= maxTokenEventsPerTx {
				continue
			}
			details[i].TokenTransfers = append(details[i].TokenTransfers, TokenTransfer{
				Token: token, Name: namePtr, Symbol: symbolPtr, Decimals: decimalsPtr, TokenLabel: tokenLabel,
				From:  "0x" + hex.EncodeToString(addr1),
				To:    "0x" + hex.EncodeToString(addr2),
				Value: valueBig.String(), LogIndex: logIndex,
			})
		} else { // Approval
			if len(details[i].Approvals) >= maxApprovalsPerTx {
				continue
			}
			details[i].Approvals = append(details[i].Approvals, TokenApproval{
				Token: token, Name: namePtr, Symbol: symbolPtr, Decimals: decimalsPtr, TokenLabel: tokenLabel,
				Owner:       "0x" + hex.EncodeToString(addr1),
				Spender:     "0x" + hex.EncodeToString(addr2),
				Amount:      valueBig.String(),
				IsUnlimited: valueBig.Cmp(maxUint256) == 0,
				LogIndex:    logIndex,
			})
		}
	}
	if err := logRows.Err(); err != nil {
		slog.Error("error iterating token events", "error", err)
	}
	return details
}

// txBatchKeys builds the SQL literal lists for a page of transactions. Hashes
// come from the database as validated hex and block numbers are integers, so
// inlining them is safe and lets ClickHouse use the primary key directly.
func txBatchKeys(txs []Transaction) (hashList, blockList string, minTime, maxTime time.Time) {
	hashes := make([]string, 0, len(txs))
	blocks := make(map[uint32]struct{}, len(txs))
	for i, tx := range txs {
		hashes = append(hashes, "unhex('"+strings.TrimPrefix(tx.Hash, "0x")+"')")
		blocks[tx.BlockNumber] = struct{}{}
		if i == 0 || tx.BlockTime.Before(minTime) {
			minTime = tx.BlockTime
		}
		if i == 0 || tx.BlockTime.After(maxTime) {
			maxTime = tx.BlockTime
		}
	}
	blockStrs := make([]string, 0, len(blocks))
	for b := range blocks {
		blockStrs = append(blockStrs, strconv.FormatUint(uint64(b), 10))
	}
	// clickhouse-go binds a plain time.Time as toDateTime(unix_seconds), which
	// drops the milliseconds of our DateTime64(3) block_time. Pad the range to
	// whole seconds so a row at 17.187 is not excluded by an upper bound of 17.000.
	// The exact block_number and transaction_hash filters keep the result precise.
	minTime = minTime.Truncate(time.Second)
	maxTime = maxTime.Truncate(time.Second).Add(time.Second)
	return strings.Join(hashes, ","), strings.Join(blockStrs, ","), minTime, maxTime
}

// handleGetTx returns a single transaction by hash
// @Summary Get transaction by hash
// @Description Get details for a specific transaction
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param hash path string true "Transaction hash (0x-prefixed)"
// @Success 200 {object} Response{data=TransactionDetail}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/txs/{hash} [get]
func (s *Server) handleGetTx(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	hashHex, err := validateTxHash(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid transaction hash")
		return
	}

	// Use unhex() in SQL for proper FixedString comparison
	row := s.conn.QueryRow(ctx, `
		SELECT `+txSelectCols+`
		FROM raw_txs
		WHERE chain_id = ? AND hash = unhex(?)
		LIMIT 1
	`, chainID, hashHex)
	tx, inputLen, err := s.scanTx(row)
	if err != nil {
		writeNotFoundError(w, "Transaction")
		return
	}

	details := s.attachTransfers(ctx, chainID, []Transaction{tx})
	detail := details[0]
	detail.Intent = s.intentFor(&detail.Transaction, &detail, inputLen > 0, "")

	writeJSON(w, http.StatusOK, Response{Data: detail})
}

// handleAddressTxs returns transactions for an address
// @Summary Get transactions by address
// @Description Get transactions sent from or to a specific address
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param address path string true "Wallet address (0x-prefixed)"
// @Param limit query int false "Number of results (max 100; 50 with include=transfers)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Param include query string false "Set to transfers to embed token_transfers, internal_txs and approvals on every row"
// @Success 200 {object} Response{data=[]Transaction,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/address/{address}/txs [get]
func (s *Server) handleAddressTxs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)
	includeTransfers := wantsTransfers(r)
	if includeTransfers && limit > includeTransfersMaxLimit {
		limit = includeTransfersMaxLimit
	}

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	addrHex, err := validateAddress(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address")
		return
	}

	fetchLimit := limit + 1

	var query string
	var args []interface{}

	if cursor != nil && cursor.HasTxIndex {
		// Cursor-based: no offset needed, more efficient UNION ALL
		query = `
			SELECT chain_id, hash, block_number, block_time, transaction_index,
				from, to, value, gas_limit, gas_price, gas_used, success, type, selector, input_len
			FROM (
				SELECT ` + txSelectCols + `
				FROM raw_txs WHERE chain_id = ? AND from = unhex(?)
				  AND (block_number, transaction_index) < (?, ?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
				UNION ALL
				SELECT ` + txSelectCols + `
				FROM raw_txs WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
				  AND (block_number, transaction_index) < (?, ?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
			)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ?
		`
		args = []interface{}{
			chainID, addrHex, cursor.BlockNumber, cursor.TxIndex, fetchLimit,
			chainID, addrHex, addrHex, cursor.BlockNumber, cursor.TxIndex, fetchLimit,
			fetchLimit,
		}
	} else {
		// Offset-based
		innerLimit := fetchLimit + offset
		query = `
			SELECT chain_id, hash, block_number, block_time, transaction_index,
				from, to, value, gas_limit, gas_price, gas_used, success, type, selector, input_len
			FROM (
				SELECT ` + txSelectCols + `
				FROM raw_txs WHERE chain_id = ? AND from = unhex(?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
				UNION ALL
				SELECT ` + txSelectCols + `
				FROM raw_txs WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
			)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{chainID, addrHex, innerLimit, chainID, addrHex, addrHex, innerLimit, fetchLimit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	txs := []Transaction{}
	inputLens := []uint64{}
	for rows.Next() {
		tx, inputLen, err := s.scanTx(rows)
		if err != nil {
			writeInternalError(w, err.Error())
			return
		}
		txs = append(txs, tx)
		inputLens = append(inputLens, inputLen)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	txs, hasMore := trimResults(txs, limit)
	inputLens = inputLens[:len(txs)]

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(txs) > 0 {
		last := txs[len(txs)-1]
		meta.NextCursor = cursorBlockTx(last.BlockNumber, last.TransactionIndex)
	}

	if wantCount {
		var total int64
		_ = s.conn.QueryRow(ctx, `
			SELECT sum(cnt) FROM (
				SELECT toInt64(count()) as cnt FROM raw_txs WHERE chain_id = ? AND from = unhex(?)
				UNION ALL
				SELECT toInt64(count()) as cnt FROM raw_txs WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
			)
		`, chainID, addrHex, chainID, addrHex, addrHex).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: s.finishTxList(ctx, chainID, txs, inputLens, includeTransfers, "0x"+addrHex),
		Meta: meta,
	})
}

// handleAddressInternalTxs returns internal transactions for an address
// @Summary Get internal transactions by address
// @Description Get internal transactions (traces) involving a specific address
// @Tags Data - EVM
// @Produce json
// @Param chainId path int true "Chain ID"
// @Param address path string true "Wallet address (0x-prefixed)"
// @Param limit query int false "Number of results (max 100)" default(20)
// @Param offset query int false "Pagination offset" default(0)
// @Success 200 {object} Response{data=[]InternalTransaction,meta=Meta}
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/data/evm/{chainId}/address/{address}/internal-txs [get]
func (s *Server) handleAddressInternalTxs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, offset := getPagination(r)
	cursor := getCursor(r)
	wantCount := getCountParam(r)

	chainID, err := getChainIDFromPath(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid chain_id")
		return
	}

	addrHex, err := validateAddress(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrInvalidParameter, "Invalid address")
		return
	}

	fetchLimit := limit + 1

	var query string
	var args []interface{}

	if cursor != nil && cursor.HasTxIndex {
		query = `
			SELECT tx_hash, block_number, block_time, trace_address,
				from, to, value, gas_used, call_type, tx_success
			FROM (
				SELECT tx_hash, block_number, block_time, trace_address, transaction_index,
					from, to, value, gas_used, call_type, tx_success
				FROM raw_traces
				WHERE chain_id = ? AND from = unhex(?)
				  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				  AND (block_number, transaction_index) < (?, ?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
				UNION ALL
				SELECT tx_hash, block_number, block_time, trace_address, transaction_index,
					from, to, value, gas_used, call_type, tx_success
				FROM raw_traces
				WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
				  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				  AND (block_number, transaction_index) < (?, ?)
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
			)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ?
		`
		args = []interface{}{
			chainID, addrHex, cursor.BlockNumber, cursor.TxIndex, fetchLimit,
			chainID, addrHex, addrHex, cursor.BlockNumber, cursor.TxIndex, fetchLimit,
			fetchLimit,
		}
	} else {
		innerLimit := fetchLimit + offset
		query = `
			SELECT tx_hash, block_number, block_time, trace_address,
				from, to, value, gas_used, call_type, tx_success
			FROM (
				SELECT tx_hash, block_number, block_time, trace_address, transaction_index,
					from, to, value, gas_used, call_type, tx_success
				FROM raw_traces
				WHERE chain_id = ? AND from = unhex(?)
				  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
				UNION ALL
				SELECT tx_hash, block_number, block_time, trace_address, transaction_index,
					from, to, value, gas_used, call_type, tx_success
				FROM raw_traces
				WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?)
				  AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				ORDER BY block_number DESC, transaction_index DESC
				LIMIT ?
			)
			ORDER BY block_number DESC, transaction_index DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{chainID, addrHex, innerLimit, chainID, addrHex, addrHex, innerLimit, fetchLimit, offset}
	}

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		writeInternalError(w, err.Error())
		return
	}
	defer rows.Close()

	internalTxs := []InternalTransaction{}
	for rows.Next() {
		var itx InternalTransaction
		var txHashBytes [32]byte
		var fromBytes [20]byte
		var toBytes []byte
		var valueBig big.Int
		var traceAddr []uint16

		if err := rows.Scan(
			&txHashBytes, &itx.BlockNumber, &itx.BlockTime, &traceAddr,
			&fromBytes, &toBytes, &valueBig, &itx.GasUsed, &itx.CallType, &itx.Success,
		); err != nil {
			writeInternalError(w, err.Error())
			return
		}

		itx.TxHash = "0x" + hex.EncodeToString(txHashBytes[:])
		itx.From = "0x" + hex.EncodeToString(fromBytes[:])
		if len(toBytes) > 0 {
			to := "0x" + hex.EncodeToString(toBytes)
			itx.To = &to
		}
		itx.Value = valueBig.String()

		traceStrs := make([]string, len(traceAddr))
		for i, v := range traceAddr {
			traceStrs[i] = fmt.Sprintf("%d", v)
		}
		itx.TraceIndex = strings.Join(traceStrs, ",")

		internalTxs = append(internalTxs, itx)
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err.Error())
		return
	}

	internalTxs, hasMore := trimResults(internalTxs, limit)

	meta := &Meta{Limit: limit, Offset: offset, HasMore: hasMore}
	if hasMore && len(internalTxs) > 0 {
		last := internalTxs[len(internalTxs)-1]
		meta.NextCursor = cursorBlockTx(last.BlockNumber, 0) // traces don't expose tx_index in result
	}

	if wantCount {
		var total int64
		_ = s.conn.QueryRow(ctx, `
			SELECT sum(cnt) FROM (
				SELECT toInt64(count()) as cnt FROM raw_traces WHERE chain_id = ? AND from = unhex(?) AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
				UNION ALL
				SELECT toInt64(count()) as cnt FROM raw_traces WHERE chain_id = ? AND to = unhex(?) AND from != unhex(?) AND (value > 0 OR call_type IN ('CREATE', 'CREATE2'))
			)
		`, chainID, addrHex, chainID, addrHex, addrHex).Scan(&total)
		meta.Total = total
	}

	writeJSON(w, http.StatusOK, Response{
		Data: internalTxs,
		Meta: meta,
	})
}
