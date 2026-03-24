# TRNGLE Local API

A local REST + WebSocket API for programmatic access to TRNGLE trading.

## Starting the API

### Standalone mode (no TUI)

```bash
trngle --api-only --port 8080
```

### Alongside the TUI

Enable in the setup wizard (step 2) or in `~/.trngle/config.json`:

```json
{
  "local_api": {
    "enabled": true,
    "port": 8080,
    "bind": "127.0.0.1",
    "auth_mode": "auto-token",
    "fixed_token": ""
  }
}
```

The API server starts automatically when the TUI launches.

## Authentication

Authentication is configured during the setup wizard or via `auth_mode` in config:

| Mode | Description |
|------|-------------|
| `"none"` | No authentication (default, suitable for single-user machines) |
| `"auto-token"` | A random token is generated on first setup |
| `"fixed-token"` | You provide your own token |

When using `"auto-token"` or `"fixed-token"`, all endpoints except `/health` require a bearer token:

```
Authorization: Bearer <your-token>
```

The token is stored in `~/.trngle/config.json` under `local_api.fixed_token` (used for both modes).

## Endpoints

### GET /health

Server status. Always accessible (no auth required).

```bash
curl http://localhost:8080/health
```

```json
{
  "status": "ok",
  "wallet": {
    "connected": true,
    "provider": "loop",
    "party_id": "party-123"
  },
  "version": "0.1.0",
  "uptime_seconds": 3600
}
```

---

### GET /balances

Current wallet balances.

```bash
curl http://localhost:8080/balances
```

```json
{
  "balances": [
    { "symbol": "CC", "instrument_id": "...", "amount": "1000.50" },
    { "symbol": "CBTC", "instrument_id": "...", "amount": "0.024" }
  ]
}
```

---

### POST /trade/quote

Request a swap quote. Returns the rate and amounts — nothing is executed yet.

```bash
curl -X POST http://localhost:8080/trade/quote \
  -H "Content-Type: application/json" \
  -d '{"from": "CC", "to": "CBTC", "amount": "100"}'
```

```json
{
  "quote_id": "QT-123456",
  "from": "CC",
  "to": "CBTC",
  "send_amount": "100.0000000000",
  "receive_amount": "0.0023809524",
  "rate": "0.0000238095",
  "expires_in": 30,
  "expires_at": "2025-03-24T14:35:30Z"
}
```

---

### POST /trade/{quote_id}/confirm

Confirm and execute a quoted trade. The entire flow (signing, on-chain submission, operator confirmation) is handled internally.

```bash
curl -X POST http://localhost:8080/trade/QT-123456/confirm
```

```json
{
  "trade_id": "TRADE-1711270500",
  "status": "submitted",
  "from": "CC",
  "to": "CBTC",
  "sent": "100.0000000000",
  "received": "0.0023809524",
  "message": "Trade submitted. Settlement is in progress."
}
```

**Error responses:**

| Status | Meaning |
|--------|---------|
| 404 | Quote not found (already used or invalid) |
| 410 | Quote expired — request a new one |
| 500 | Trade execution failed |

---

### GET /trades

List trades with optional pagination and filters.

| Param | Default | Description |
|-------|---------|-------------|
| `limit` | 20 | Max results (1-100) |
| `offset` | 0 | Skip N results |
| `type` | — | Filter: `trade`, `transfer_in`, `transfer_out` |
| `asset` | — | Filter by from_asset symbol |

```bash
curl "http://localhost:8080/trades?limit=10&type=trade"
```

```json
{
  "trades": [ ... ],
  "total": 42,
  "limit": 10,
  "offset": 0
}
```

---

### GET /trades/{id}

Get a single trade by its ID.

```bash
curl http://localhost:8080/trades/tx-20250324T143500.123456789
```

Returns the full trade record including status, assets, amounts, timestamps, and error details if any.

---

### GET /transfers

List pending incoming transfers waiting to be accepted.

```bash
curl http://localhost:8080/transfers
```

```json
{
  "transfers": [
    {
      "id": "xfer-abc",
      "from": "alice-party",
      "amount": "50",
      "asset": "CC",
      "expires_at": "2025-03-24T15:00:00Z"
    }
  ]
}
```

---

### POST /transfers/{id}/accept

Accept a pending incoming transfer.

```bash
curl -X POST http://localhost:8080/transfers/xfer-abc/accept
```

```json
{
  "status": "accepted",
  "asset": "CC",
  "amount": "50",
  "from": "alice-party",
  "message": "Transfer accepted."
}
```

---

## WebSocket: /ws

Connect to receive real-time trade lifecycle events.

```
ws://localhost:8080/ws
```

### Filtering

Subscribe to events for a specific trade:

```
ws://localhost:8080/ws?trade_id=TRADE-1711270500
```

When connecting with a `trade_id` filter, the server replays any historical events for that trade before streaming live events.

### Event format

Events are JSON objects matching the operator's notification schema:

```json
{
  "type": "maker_confirmed",
  "quote_id": "QT-123456",
  "trade_id": "TRADE-1711270500",
  "status": "submitted",
  "trade_state": "maker_confirmed"
}
```

### Event types

| Type | Meaning |
|------|---------|
| `taker_confirmed` | Your on-chain confirmation was verified |
| `maker_confirmed` | Counterparty confirmed the trade |
| `trade_settled` | Trade completed successfully |
| `settlement_completed` | Settlement finalized on ledger |
| `settlement_failed` | Settlement failed |
| `maker_error` | Counterparty reported an error |
| `trade_expired` | Trade timed out |
| `trade_cleanup_complete` | Tokens refunded after failure |

### Replayed events

When connecting with `?trade_id=X`, historical events include `"replayed": "true"` so you can distinguish them from live events.

## Error format

All errors return:

```json
{
  "error": "error_code",
  "message": "Human-readable description."
}
```

## Typical workflow

```bash
# 1. Check you're connected
curl http://localhost:8080/health

# 2. Check your balances
curl http://localhost:8080/balances

# 3. Get a quote
curl -X POST http://localhost:8080/trade/quote \
  -d '{"from":"CC","to":"CBTC","amount":"100"}'

# 4. Confirm the trade (use quote_id from step 3)
curl -X POST http://localhost:8080/trade/QT-123456/confirm

# 5. Monitor via WebSocket or poll /trades
```
