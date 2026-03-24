package tui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/trngle-xyz/cli/internal/config"
	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/wallet"
	"github.com/trngle-xyz/cli/internal/wallet/loop"
)

type tradeWallet interface {
	wallet.WalletAdapter
	Authenticate(partyID, apiURL string) error
}

var (
	newQuoteClientFn = core.NewQuoteClient // func(baseURL, apiKey string) QuoteClient
	newTradeWalletFn = func() tradeWallet { return loop.NewAdapter() }
)

type walletInitMsg struct {
	address string
}

type gasPollerReadyMsg struct {
	poller *loop.Adapter
}

type apiHealthMsg struct {
	healthy  bool
	endpoint string
	token    string
}

type errMsg struct {
	err error
}

type shutdownResultMsg struct {
	apiAttempted bool
	apiStopped   bool
	err          error
}

// apiStartedMsg is sent after we attempt to start the local API server post-wizard.
type apiStartedMsg struct{}

// StartAPIServerFunc is set by main.go to allow the TUI model to trigger
// the API server start after the setup wizard completes.
var StartAPIServerFunc func(cfg config.AppConfig)

// ShutdownAPIServerFunc is set by main.go so the TUI can perform
// graceful verified shutdown before quitting.
var ShutdownAPIServerFunc func() error

// startGasPollerCmd creates a persistent Loop adapter and starts its background
// gas poller. Returns gasPollerReadyMsg so the Model can store it.
func startGasPollerCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		if !useLoopWalletProvider(cfg) {
			return gasPollerReadyMsg{} // no poller needed for non-Loop wallets
		}
		privKey := strings.TrimSpace(cfg.PrivateKeyHex)
		if privKey == "" {
			return gasPollerReadyMsg{}
		}
		partyID := strings.TrimSpace(cfg.WalletPartyID)
		if partyID == "" {
			return gasPollerReadyMsg{}
		}
		adapter := loop.NewAdapter()
		if err := adapter.Initialize(privKey); err != nil {
			return gasPollerReadyMsg{}
		}
		apiURL := resolveWalletAPIURL(cfg)
		if err := adapter.Authenticate(partyID, apiURL); err != nil {
			return gasPollerReadyMsg{}
		}
		adapter.StartGasPoller()
		adapter.StartAuthRefresh()
		return gasPollerReadyMsg{poller: adapter}
	}
}

// startAPIAfterSetupCmd starts the API server and then checks health.
func startAPIAfterSetupCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		if StartAPIServerFunc != nil && cfg.LocalAPI.Enabled {
			StartAPIServerFunc(cfg)
		}
		return apiStartedMsg{}
	}
}

// initializeWalletCmd validates the config has a PK and party ID,
// then returns the stored party ID as the wallet address.
// The actual Loop auth was already done during the setup wizard —
// we don't re-authenticate on every TUI launch.
func initializeWalletCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		// Small delay so the spinner is visible (feels like it's doing something).
		time.Sleep(300 * time.Millisecond)

		if cfg.PrivateKeyHex == "" {
			return errMsg{err: fmt.Errorf("no private key configured — run setup")}
		}
		if cfg.WalletPartyID == "" {
			return errMsg{err: fmt.Errorf("no party ID configured — run setup")}
		}

		return walletInitMsg{
			address: cfg.WalletPartyID,
		}
	}
}

// balancesResultMsg carries the result of an async balance fetch.
type balancesResultMsg struct {
	balances []wallet.Balance
	err      error
}

type quotePrefetchResultMsg struct {
	quote     *core.Quote
	acceptCtx *core.AcceptContext
	err       error
}

type quoteAcceptContextReadyMsg struct {
	quoteID   string
	acceptCtx *core.AcceptContext
	err       error
}

type quoteMergeNeededMsg struct {
	holdings []wallet.HoldingContract // all matching unlocked holdings to merge
	total    float64
	asset    string
}

type quoteMergeCompleteMsg struct {
	single *wallet.HoldingContract
}

type quoteHoldingsValidMsg struct {
	single *wallet.HoldingContract
}

type quoteSubmitResultMsg struct {
	quoteID      string
	tradeCID     string
	allocationID string
	confirmErr   error
	submitErr    error
}

// fetchBalancesCmd authenticates with the configured wallet provider and fetches fresh holdings.
func fetchBalancesCmd(cfg config.AppConfig, preferredPartyID string) tea.Cmd {
	return func() tea.Msg {
		authPartyID := strings.TrimSpace(cfg.WalletPartyID)
		if !useLoopWalletProvider(cfg) {
			if v := strings.TrimSpace(preferredPartyID); v != "" {
				authPartyID = v
			} else if v := strings.TrimSpace(cfg.QuotePartyID); v != "" {
				authPartyID = v
			}
		}

		if strings.TrimSpace(authPartyID) == "" {
			return balancesResultMsg{err: fmt.Errorf("wallet party id not configured")}
		}
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return balancesResultMsg{err: fmt.Errorf("wallet private key not configured")}
		}

		adapter := walletAdapterForConfig(cfg)
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return balancesResultMsg{err: fmt.Errorf("key error: %w", err)}
		}

		apiURL := resolveWalletAPIURL(cfg)
		if err := adapter.Authenticate(authPartyID, apiURL); err != nil {
			return balancesResultMsg{err: fmt.Errorf("auth failed: %w", err)}
		}

		balances, err := adapter.GetBalances()
		if err != nil {
			return balancesResultMsg{err: err}
		}
		return balancesResultMsg{balances: balances}
	}
}

// checkAPIHealthCmd checks if the local API server is reachable.
// Retries a few times with short delays to allow the background server to bind.
func checkAPIHealthCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		if !cfg.LocalAPI.Enabled {
			return apiHealthMsg{healthy: false, endpoint: "disabled"}
		}

		endpoint := fmt.Sprintf("http://%s:%d", cfg.LocalAPI.Bind, cfg.LocalAPI.Port)

		token := ""
		if cfg.LocalAPI.AuthMode == "auto-token" && cfg.LocalAPI.FixedToken != "" {
			token = cfg.LocalAPI.FixedToken
		}

		// Retry up to 10 times (total ~5s) to let the server finish binding.
		healthy := false
		client := &http.Client{Timeout: 1 * time.Second}
		for i := 0; i < 10; i++ {
			resp, err := client.Get(endpoint + "/health")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					healthy = true
					break
				}
			}
			time.Sleep(500 * time.Millisecond)
		}

		return apiHealthMsg{
			healthy:  healthy,
			endpoint: endpoint,
			token:    token,
		}
	}
}

func shutdownAppCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		if !cfg.LocalAPI.Enabled {
			return shutdownResultMsg{apiAttempted: false, apiStopped: true}
		}

		if ShutdownAPIServerFunc == nil {
			return shutdownResultMsg{apiAttempted: true, apiStopped: false, err: fmt.Errorf("shutdown callback not configured")}
		}

		if err := ShutdownAPIServerFunc(); err != nil {
			return shutdownResultMsg{apiAttempted: true, apiStopped: false, err: err}
		}

		// Verify the API is actually down.
		endpoint := fmt.Sprintf("http://%s:%d", cfg.LocalAPI.Bind, cfg.LocalAPI.Port)
		client := &http.Client{Timeout: 500 * time.Millisecond}
		for i := 0; i < 8; i++ {
			resp, err := client.Get(endpoint + "/health")
			if err != nil {
				return shutdownResultMsg{apiAttempted: true, apiStopped: true}
			}
			resp.Body.Close()
			time.Sleep(150 * time.Millisecond)
		}
		return shutdownResultMsg{apiAttempted: true, apiStopped: false, err: fmt.Errorf("api still responding after shutdown attempt")}
	}
}

// prefetchQuoteCmd requests a quote from the operator.  The accept context
// (trade creation on-chain) is fetched lazily when the user presses Y, so
// no on-chain work happens until the user confirms.
func prefetchQuoteCmd(cfg config.AppConfig, from, to, amount, partyID string) tea.Cmd {
	return func() tea.Msg {
		client := newQuoteClientFn(cfg.TrngleAPIURL, cfg.TrngleAPIKey)
		quoteParty := resolveQuotePartyID(cfg, partyID)
		quote, err := client.RequestQuote(context.Background(), from, to, amount, quoteParty)
		if err != nil {
			return quotePrefetchResultMsg{err: err}
		}
		return quotePrefetchResultMsg{quote: quote}
	}
}

func resolveQuotePartyID(cfg config.AppConfig, fallback string) string {
	if v := strings.TrimSpace(cfg.QuotePartyID); v != "" {
		return v
	}
	if v := strings.TrimSpace(fallback); v != "" {
		return v
	}
	if v := strings.TrimSpace(cfg.WalletPartyID); v != "" {
		return v
	}
	return ""
}

func resolveWalletAPIURL(cfg config.AppConfig) string {
	if v := strings.TrimSpace(os.Getenv("TRNGLE_LOOP_API_URL")); v != "" {
		return v
	}
	network := strings.ToLower(strings.TrimSpace(cfg.Network))
	if network == "" {
		network = "mainnet"
	}
	if v := strings.TrimSpace(loop.NetworkAPIURLs[network]); v != "" {
		return v
	}
	return loop.NetworkAPIURLs["mainnet"]
}

func walletAdapterForConfig(_ config.AppConfig) tradeWallet {
	return newTradeWalletFn()
}

func useLoopWalletProvider(_ config.AppConfig) bool {
	return true
}

// fetchAcceptContextCmd calls the operator /accept endpoint to create the
// trade on-chain and returns the resulting AcceptContext. No wallet interaction
// happens here — that is deferred to submitOnChainCmd.
func fetchAcceptContextCmd(cfg config.AppConfig, quoteID string) tea.Cmd {
	return func() tea.Msg {
		client := newQuoteClientFn(cfg.TrngleAPIURL, cfg.TrngleAPIKey)
		acceptCtx, err := client.AcceptQuoteContext(context.Background(), quoteID)
		if err != nil {
			return quoteAcceptContextReadyMsg{quoteID: quoteID, err: fmt.Errorf("accept quote: %w", err)}
		}
		return quoteAcceptContextReadyMsg{quoteID: quoteID, acceptCtx: acceptCtx}
	}
}

// submitOnChainCmd performs wallet auth, holding selection, registry factory
// lookup, and on-chain allocation + taker-confirm submission. It expects
// activeQuote.AcceptContext to already be populated.
func submitOnChainCmd(cfg config.AppConfig, activeQuote *ActiveQuote) tea.Cmd {
	return func() tea.Msg {
		if activeQuote == nil || activeQuote.AcceptContext == nil {
			quoteID := ""
			if activeQuote != nil {
				quoteID = activeQuote.ID
			}
			return quoteSubmitResultMsg{quoteID: quoteID, submitErr: fmt.Errorf("missing accept context")}
		}

		adapter := walletAdapterForConfig(cfg)
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet private key not configured")}
		}
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("key error: %w", err)}
		}
		apiURL := resolveWalletAPIURL(cfg)
		authPartyID := strings.TrimSpace(cfg.WalletPartyID)
		if !useLoopWalletProvider(cfg) {
			if takerParty := strings.TrimSpace(activeQuote.AcceptContext.TakerParty); takerParty != "" {
				authPartyID = takerParty
			}
		}
		if strings.TrimSpace(authPartyID) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet party id not configured")}
		}
		if err := adapter.Authenticate(authPartyID, apiURL); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("auth failed: %w", err)}
		}

		// Pre-check holdings to detect merge requirement before proceeding.
		allHoldings, err := adapter.GetHoldingContracts("")
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("fetch holdings: %w", err)}
		}
		single, mergeSlice, selErr := checkMergeNeeded(allHoldings, activeQuote.AcceptContext.TakerLeg.Asset, activeQuote.AcceptContext.TakerLeg.Amount)
		if selErr != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: selErr}
		}
		if single == nil {
			// No single holding is sufficient — signal merge needed.
			var total float64
			for _, h := range mergeSlice {
				if v, err := strconv.ParseFloat(strings.TrimSpace(h.Amount), 64); err == nil {
					total += v
				}
			}
			return quoteMergeNeededMsg{
				holdings: mergeSlice,
				total:    total,
				asset:    activeQuote.AcceptContext.TakerLeg.Asset,
			}
		}

		return quoteHoldingsValidMsg{single: single}
	}
}

// executeQuoteSubmitCmd is the tea.Cmd triggered when the user presses Y.
// It first fetches the accept context (creating the trade on-chain via the
// operator), then resolves the allocation factory via the Canton registry,
// and finally submits the allocation + taker-confirm commands.
func executeQuoteSubmitCmd(cfg config.AppConfig, activeQuote *ActiveQuote) tea.Cmd {
	return func() tea.Msg {
		if activeQuote == nil {
			return quoteSubmitResultMsg{submitErr: fmt.Errorf("missing active quote")}
		}

		adapter := walletAdapterForConfig(cfg)
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet private key not configured")}
		}
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("key error: %w", err)}
		}
		apiURL := resolveWalletAPIURL(cfg)
		authPartyID := strings.TrimSpace(cfg.WalletPartyID)
		if !useLoopWalletProvider(cfg) && activeQuote.AcceptContext != nil {
			if takerParty := strings.TrimSpace(activeQuote.AcceptContext.TakerParty); takerParty != "" {
				authPartyID = takerParty
			}
		}
		if strings.TrimSpace(authPartyID) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet party id not configured")}
		}
		if err := adapter.Authenticate(authPartyID, apiURL); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("auth failed: %w", err)}
		}

		client := newQuoteClientFn(cfg.TrngleAPIURL, cfg.TrngleAPIKey)

		// Fetch accept context now (operator creates the trade on-chain).
		// This is the first on-chain action — triggered by the user pressing Y.
		if activeQuote.AcceptContext == nil {
			acceptCtx, err := client.AcceptQuoteContext(context.Background(), activeQuote.ID)
			if err != nil {
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("accept quote: %w", err)}
			}
			activeQuote.AcceptContext = acceptCtx

			// Now that we have the taker party, re-auth with the correct party.
			if !useLoopWalletProvider(cfg) {
				if takerParty := strings.TrimSpace(acceptCtx.TakerParty); takerParty != "" && !strings.EqualFold(takerParty, authPartyID) {
					if authErr := adapter.Authenticate(takerParty, apiURL); authErr != nil {
						return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("re-auth as taker: %w", authErr)}
					}
				}
			}
		}

		return executeAcceptContextSubmit(client, adapter, activeQuote, nil, cfg.Network)
	}
}
