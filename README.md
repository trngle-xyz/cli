# trngle

A terminal app for swapping tokens on [Canton Network](https://www.canton.network/).

Trade CC (Amulet), CBTC, and USDXLR directly from your terminal — no browser, no extensions, no middleman. Connect your Canton wallet, get a quote, confirm, done.

## Status

**Testnet** — currently running on Canton Network testnet. Mainnet support is planned.

This is early software. You're trading on testnet with test tokens. Expect rough edges, report bugs.

## Install

**Mac / Linux:**

```sh
curl -fsSL https://raw.githubusercontent.com/trngle-xyz/cli/main/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/trngle-xyz/cli/main/install.ps1 | iex
```

No dependencies required. The install script downloads a single binary and puts it in your PATH.

## Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/trngle-xyz/cli/main/uninstall.sh | sh
```

This removes the binary and optionally your config/history at `~/.trngle/`.

## Quick Start

```
trngle
```

On first run, the setup wizard walks you through:

1. **Network** — select testnet
2. **Wallet** — connect your Canton Loop wallet (ed25519 key + party ID)
3. **API** — optional local REST API for programmatic access

Once connected, you can trade:

```
❯ quote 100 CC to CBTC
```

The app fetches a live quote from the operator, shows you the rate, and waits for your confirmation. Press **Y** to execute the swap on-chain.

## Commands

| Command | Description |
|---------|-------------|
| `quote <amount> <from> [to] <to>` | Get a swap quote (e.g. `quote 100 CC to CBTC`) |
| `balance` | Show wallet balances |
| `address` | Show your wallet address |
| `history` | View past trades |
| `settings` | Open settings |
| `help` | Show available commands |
| `quit` | Exit |

## Supported Assets

| Asset | Description |
|-------|-------------|
| CC | Canton Coin (Amulet) |
| CBTC | Canton BTC |
| USDXLR | Canton USD stablecoin |

## How It Works

trngle connects to the Canton Network through an operator API. When you request a quote:

1. The operator finds a counterparty and returns a price
2. You review the quote in your terminal
3. On confirmation, trngle signs and submits the trade on-chain via your Loop wallet
4. The operator settles the trade — both sides receive their tokens

All signing happens locally. Your private key never leaves your machine.

## Configuration

Config is stored at `~/.trngle/config.json`. Trade history is in `~/.trngle/history.db`.

You can override the operator API URL:

```
trngle --trngle-api-url https://your-api.example.com
```

## Building from Source

```
git clone https://github.com/trngle-xyz/cli.git
cd cli
make build
./trngle
```

Requires Go 1.21+.

## License

MIT
