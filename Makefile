# Configure these in your environment or .env.local (not committed)
PROD_HOST ?= $(error Set PROD_HOST, e.g., export PROD_HOST=root@your-server-ip)
PROD_URL ?= $(error Set PROD_URL, e.g., export PROD_URL=http://your-server-ip:8080)
PROD_DIR := /root/clickhouse-metrics-poc
BINARY := icicle

.PHONY: build deploy deploy-api deploy-indexer logs-api logs-indexer status test install-services stop start restart

# Local build
build:
	go build -o $(BINARY) .

# Full deploy: build on server, restart both services
deploy:
	ssh $(PROD_HOST) 'cd $(PROD_DIR) && git pull && go build -o $(BINARY) . && systemctl restart icicle-api icicle-indexer'

# Deploy API only
deploy-api:
	ssh $(PROD_HOST) 'cd $(PROD_DIR) && git pull && go build -o $(BINARY) . && systemctl restart icicle-api'

# Deploy indexer only
deploy-indexer:
	ssh $(PROD_HOST) 'cd $(PROD_DIR) && git pull && go build -o $(BINARY) . && systemctl restart icicle-indexer'

# View API logs (follow mode)
logs-api:
	ssh $(PROD_HOST) 'journalctl -u icicle-api -f'

# View indexer logs (follow mode)
logs-indexer:
	ssh $(PROD_HOST) 'journalctl -u icicle-indexer -f'

# View last 100 lines of both
logs:
	ssh $(PROD_HOST) 'journalctl -u icicle-api -u icicle-indexer -n 100'

# Check service status
status:
	ssh $(PROD_HOST) 'systemctl status icicle-api icicle-indexer --no-pager'

# Quick API test
test:
	@echo "Testing blocks endpoint..."
	@curl -s "$(PROD_URL)/api/v1/data/evm/43114/blocks?limit=1" | jq -r '.data[0] | "Block \(.block_number): \(.tx_count) txs"'

# Install systemd services (run once on server)
install-services:
	ssh $(PROD_HOST) 'cd $(PROD_DIR) && cp deploy/*.service /etc/systemd/system/ && systemctl daemon-reload && systemctl enable icicle-api icicle-indexer'

# Stop both services
stop:
	ssh $(PROD_HOST) 'systemctl stop icicle-api icicle-indexer'

# Start both services
start:
	ssh $(PROD_HOST) 'systemctl start icicle-api icicle-indexer'

# Restart both services
restart:
	ssh $(PROD_HOST) 'systemctl restart icicle-api icicle-indexer'
