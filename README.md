# trngle (testnet)

A terminal app for trading tokens on [Canton Network](https://www.canton.network/). Currently integrated with **Loop wallets** — more wallet support coming.

trngle runs entirely on your machine. Your keys stay local, the code runs local, and the only external call is to the operator API for quotes and settlement. Nothing is custodial — you sign every trade yourself from your own wallet.

Your keys. Your terminal. Your trades.

## Status

**Testnet only** — this build runs on Canton Network testnet with test tokens. Mainnet support is planned.

Early software. Expect rough edges, report bugs.

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

**Mac / Linux:**

```sh
curl -fsSL https://raw.githubusercontent.com/trngle-xyz/cli/main/uninstall.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/trngle-xyz/cli/main/uninstall.ps1 | iex
```

This removes the binary and optionally your config/history at `~/.trngle/`.

## Quick Start

```
trngle
```

On first run, the setup wizard walks you through:

1. **Network** — select testnet
2. **Wallet** — connect your Loop wallet (ed25519 key + party ID)
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

trngle is a self-contained binary that runs on your computer. When you request a trade:

1. trngle calls the operator API for a quote
2. You review the price in your terminal
3. On confirmation, trngle signs the trade locally with your private key and submits it on-chain via your Loop wallet
4. The operator settles the trade — both sides receive their tokens

The operator provides liquidity and settlement. trngle provides the interface. All cryptographic signing happens on your machine — your private key never leaves your computer and is never sent to the operator or anyone else.

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
