#!/usr/bin/env bash
# Compare two Avalanche C-Chain RPC endpoints on latency and correctness.
#
# Usage:
#   ./scripts/compare_rpc.sh                # default: ours vs api.avax.network
#   ITERATIONS=50 ./scripts/compare_rpc.sh  # override iteration count
#   A_URL=... B_URL=... ./scripts/compare_rpc.sh

set -uo pipefail

A_NAME="${A_NAME:-Ours}"
A_URL="${A_URL:-https://rpc.l1beat.io/ext/bc/C/rpc}"
B_NAME="${B_NAME:-Public}"
B_URL="${B_URL:-https://api.avax.network/ext/bc/C/rpc}"
ITERATIONS="${ITERATIONS:-30}"

# Colors
G=$'\e[32m'; R=$'\e[31m'; Y=$'\e[33m'; B=$'\e[1m'; N=$'\e[0m'

if ! command -v jq >/dev/null 2>&1; then
    echo "${R}jq is required.${N} Install: brew install jq  (or apt install jq)"
    exit 1
fi

# A burner address with non-zero balance, just so eth_getBalance returns something realistic.
SAMPLE_ADDR="0x8eB8a3b98659Cce290402893d0123abb75E3ab28"

# Methods to test: label|json-payload
METHODS=(
    "eth_chainId|{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_chainId\",\"params\":[]}"
    "eth_blockNumber|{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_blockNumber\",\"params\":[]}"
    "eth_gasPrice|{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_gasPrice\",\"params\":[]}"
    "eth_getBalance|{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_getBalance\",\"params\":[\"${SAMPLE_ADDR}\",\"latest\"]}"
    "eth_getBlockByNumber_latest|{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_getBlockByNumber\",\"params\":[\"latest\",false]}"
    "eth_getBlockByNumber_full|{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_getBlockByNumber\",\"params\":[\"0x2710\",true]}"
)

# stats <name> <url> <payload>  -> echoes "ok_count avg p50 p95 min max sample_result"
stats() {
    local url="$1" payload="$2"
    local times=() ok=0 last_result=""
    for ((i=0; i<ITERATIONS; i++)); do
        local resp time
        resp=$(curl -s -o /tmp/_rpc_body.$$ -w "%{time_total}" \
            -X POST -H "Content-Type: application/json" \
            --data "$payload" --max-time 10 "$url" 2>/dev/null) || resp=""
        time="$resp"
        if [[ -n "$time" ]] && jq -e '.result // .error' /tmp/_rpc_body.$$ >/dev/null 2>&1; then
            if jq -e '.result' /tmp/_rpc_body.$$ >/dev/null 2>&1; then
                ok=$((ok+1))
                last_result=$(jq -c '.result' /tmp/_rpc_body.$$)
            fi
            times+=("$time")
        fi
    done
    rm -f /tmp/_rpc_body.$$

    if [[ ${#times[@]} -eq 0 ]]; then
        echo "0 - - - - - ERROR"
        return
    fi

    printf '%s\n' "${times[@]}" | sort -n | awk -v ok="$ok" -v sample="$last_result" '
        { a[NR]=$1; sum+=$1 }
        END {
            n=NR
            min=a[1]; max=a[n]
            i50=int(n*0.5); if(i50<1)i50=1
            i95=int(n*0.95); if(i95<1)i95=1; if(i95>n)i95=n
            p50=a[i50]; p95=a[i95]
            avg=sum/n
            printf "%d %.3f %.3f %.3f %.3f %.3f %s\n", ok, avg, p50, p95, min, max, sample
        }'
}

print_header() {
    printf "${B}%-32s %-10s %8s %8s %8s %8s %8s  %s${N}\n" \
        "Method" "Endpoint" "OK" "avg(s)" "p50" "p95" "max" "result"
    printf '%.0s-' {1..120}; echo
}

print_row() {
    local method="$1" name="$2" line="$3" color="$4"
    read -r ok avg p50 p95 mn mx sample <<< "$line"
    local short="${sample:0:30}"
    [[ ${#sample} -gt 30 ]] && short="${short}..."
    printf "${color}%-32s %-10s %8s %8s %8s %8s %8s  %s${N}\n" \
        "$method" "$name" "${ok}/${ITERATIONS}" "$avg" "$p50" "$p95" "$mx" "$short"
}

echo "${B}Comparing ${A_NAME} vs ${B_NAME}${N}"
echo "  ${A_NAME}:   $A_URL"
echo "  ${B_NAME}: $B_URL"
echo "  Iterations per method: $ITERATIONS"
echo

print_header

a_wins=0; b_wins=0; mismatches=0

for entry in "${METHODS[@]}"; do
    method="${entry%%|*}"
    payload="${entry#*|}"

    a_line=$(stats "$A_URL" "$payload")
    b_line=$(stats "$B_URL" "$payload")

    a_avg=$(echo "$a_line" | awk '{print $2}')
    b_avg=$(echo "$b_line" | awk '{print $2}')
    a_sample=$(echo "$a_line" | awk '{$1=$2=$3=$4=$5=$6=""; print substr($0,7)}')
    b_sample=$(echo "$b_line" | awk '{$1=$2=$3=$4=$5=$6=""; print substr($0,7)}')

    # Color the faster side green
    a_color="$N"; b_color="$N"
    if awk "BEGIN{exit !($a_avg < $b_avg)}" 2>/dev/null; then
        a_color="$G"; a_wins=$((a_wins+1))
    elif awk "BEGIN{exit !($b_avg < $a_avg)}" 2>/dev/null; then
        b_color="$G"; b_wins=$((b_wins+1))
    fi

    print_row "$method" "$A_NAME" "$a_line" "$a_color"
    print_row "$method" "$B_NAME" "$b_line" "$b_color"

    # For deterministic methods, flag if results disagree
    case "$method" in
        eth_chainId|eth_getBlockByNumber_full)
            if [[ "$a_sample" != "$b_sample" ]] && [[ "$a_sample" != *ERROR* ]] && [[ "$b_sample" != *ERROR* ]]; then
                echo "  ${R}! result mismatch on ${method}${N}"
                mismatches=$((mismatches+1))
            fi
            ;;
    esac
    echo
done

echo "${B}Summary${N}"
echo "  ${A_NAME} faster on:   $a_wins methods"
echo "  ${B_NAME} faster on: $b_wins methods"
[[ $mismatches -gt 0 ]] && echo "  ${R}Mismatches: $mismatches${N}" || echo "  ${G}All deterministic results match${N}"
