package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/trngle-xyz/cli/internal/config"
	"github.com/trngle-xyz/cli/internal/wallet"
)

// mergeHoldingsCmd performs a self-transfer to consolidate multiple holdings
// into a single one, then calls executeAcceptContextSubmit with the result.
func mergeHoldingsCmd(cfg config.AppConfig, activeQuote *ActiveQuote, holdings []wallet.HoldingContract) tea.Cmd {
	return func() tea.Msg {
		if activeQuote == nil || activeQuote.AcceptContext == nil {
			return quoteSubmitResultMsg{submitErr: fmt.Errorf("missing accept context for merge")}
		}

		adapter := walletAdapterForConfig(cfg)
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet private key not configured")}
		}
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("key error: %w", err)}
		}
		apiURL := resolveWalletAPIURL(cfg)
		takerParty := strings.TrimSpace(activeQuote.AcceptContext.TakerParty)
		if takerParty == "" {
			takerParty = strings.TrimSpace(cfg.WalletPartyID)
		}
		if takerParty == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet party id not configured")}
		}
		if err := adapter.Authenticate(takerParty, apiURL); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("auth failed: %w", err)}
		}

		authToken := ""
		if tokenHolder, ok := adapter.(interface{ AuthToken() string }); ok {
			authToken = tokenHolder.AuthToken()
		}

		// Re-fetch holdings fresh — the original CIDs from the merge msg may be
		// stale if the background gas poller consumed a holding in the interim.
		freshHoldings, err := adapter.GetHoldingContracts("")
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("re-fetch holdings for merge: %w", err)}
		}
		asset := activeQuote.AcceptContext.TakerLeg.Asset
		amount := activeQuote.AcceptContext.TakerLeg.Amount
		_, mergeSlice, selErr := checkMergeNeeded(freshHoldings, asset, amount)
		if selErr != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("merge re-check: %w", selErr)}
		}
		// Use the fresh holdings for the merge
		holdings = mergeSlice

		var total float64
		for _, h := range holdings {
			if v, err := strconv.ParseFloat(strings.TrimSpace(h.Amount), 64); err == nil {
				total += v
			}
		}

		tfResult, err := fetchTransferFactory(authToken, holdings, takerParty, total, cfg.Network)
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("transfer factory: %w", err)}
		}

		now := time.Now().UTC()
		cids := make([]string, len(holdings))
		for i, h := range holdings {
			cids[i] = h.ContractID
		}
		primary := holdings[0]
		transferPayload := map[string]any{
			"sender":   takerParty,
			"receiver": takerParty,
			"amount":   strconv.FormatFloat(total, 'f', 10, 64),
			"instrumentId": map[string]any{
				"admin": primary.InstrumentAdmin,
				"id":    primary.InstrumentID,
			},
			"lock":             nil,
			"requestedAt":      now.Format(time.RFC3339),
			"executeBefore":    now.Add(24 * time.Hour).Format(time.RFC3339),
			"meta":             map[string]any{"values": map[string]any{}},
			"inputHoldingCids": cids,
		}
		mergeCmd := map[string]any{
			"ExerciseCommand": map[string]any{
				"templateId": transferFactoryTemplateID,
				"contractId": tfResult.FactoryID,
				"choice":     "TransferFactory_Transfer",
				"choiceArgument": map[string]any{
					"expectedAdmin": primary.InstrumentAdmin,
					"transfer":      transferPayload,
					"extraArgs": map[string]any{
						"context": tfResult.ChoiceContextData,
						"meta":    map[string]any{"values": map[string]any{}},
					},
				},
			},
		}
		mergePayload := wallet.CommandPayload{
			Commands:           []any{mergeCmd},
			DisclosedContracts: tfResult.DisclosedContracts,
			ActAs:              []string{takerParty},
			ReadAs:             []string{takerParty},
		}
		if _, err := adapter.PrepareAndSubmit(mergePayload); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("merge self-transfer: %w", err)}
		}

		// Re-fetch holdings — the merge transaction is committed but the new
		// holding may not be visible immediately. Poll a few times.
		var single *wallet.HoldingContract
		for poll := 0; poll < 5; poll++ {
			if poll > 0 {
				time.Sleep(2 * time.Second)
			}
			allHoldings, err := adapter.GetHoldingContracts("")
			if err != nil {
				continue
			}
			s, _, selErr := checkMergeNeeded(allHoldings, activeQuote.AcceptContext.TakerLeg.Asset, activeQuote.AcceptContext.TakerLeg.Amount)
			if selErr != nil {
				continue
			}
			if s != nil {
				single = s
				break
			}
		}
		if single == nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("merge completed but still no single holding large enough")}
		}

		return quoteMergeCompleteMsg{single: single}
	}
}

// submitWithHoldingCmd calls executeAcceptContextSubmit with a pre-selected holding.
// Used after a merge to run the allocation step without re-fetching holdings.
func submitWithHoldingCmd(cfg config.AppConfig, activeQuote *ActiveQuote, single *wallet.HoldingContract) tea.Cmd {
	return func() tea.Msg {
		adapter := walletAdapterForConfig(cfg)
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet private key not configured")}
		}
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("key error: %w", err)}
		}
		apiURL := resolveWalletAPIURL(cfg)
		takerParty := strings.TrimSpace(activeQuote.AcceptContext.TakerParty)
		if takerParty == "" {
			takerParty = strings.TrimSpace(cfg.WalletPartyID)
		}
		if err := adapter.Authenticate(takerParty, apiURL); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("auth failed: %w", err)}
		}
		client := newQuoteClientFn(cfg.TrngleAPIURL, cfg.TrngleAPIKey)
		return executeAcceptContextSubmit(client, adapter, activeQuote, single, cfg.Network)
	}
}

const allocationInterfaceID = "#splice-api-token-allocation-v1:Splice.Api.Token.AllocationV1:Allocation"

// pollForNewAllocation queries the Allocation interface to find a new allocation
// that appeared after the allocate command. The Loop SDK returns minimal
// transaction results without a full transaction tree, so we query directly.
// Proven approach: new allocations typically appear within 2-4 seconds.
func pollForNewAllocation(adapter tradeWallet, original *wallet.HoldingContract, before map[string]bool) (string, string) {
	var lastErr error
	var lastCount int
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		allocs, err := adapter.GetHoldingContracts(allocationInterfaceID)
		if err != nil {
			lastErr = err
			continue
		}
		lastCount = len(allocs)
		for _, a := range allocs {
			if before[a.ContractID] {
				continue
			}
			return a.ContractID, ""
		}
	}
	if lastErr != nil {
		return "", fmt.Sprintf("poll failed after 10 attempts, last error: %v", lastErr)
	}
	return "", fmt.Sprintf("poll found %d allocations (%d before), none new after 10 attempts", lastCount, len(before))
}

// checkMergeNeeded evaluates available holdings for a given asset/amount.
// Returns (single, nil, nil) if one holding covers the required amount.
// Returns (nil, allMatching, nil) if total across unlocked holdings is sufficient but no single one is.
// Returns (nil, nil, err) if total is insufficient.
func checkMergeNeeded(holdings []wallet.HoldingContract, asset, amount string) (*wallet.HoldingContract, []wallet.HoldingContract, error) {
	if len(holdings) == 0 {
		return nil, nil, fmt.Errorf("no holdings available for signing")
	}

	targetAsset := strings.ToUpper(strings.TrimSpace(asset))
	if targetAsset == "CC" {
		targetAsset = "AMULET"
	}

	required, err := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	if err != nil || required <= 0 {
		return nil, nil, fmt.Errorf("invalid taker amount %q", amount)
	}

	var matching []wallet.HoldingContract
	var total float64
	for _, h := range holdings {
		if h.IsLocked {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(h.InstrumentID)) != targetAsset {
			continue
		}
		avail, err := strconv.ParseFloat(strings.TrimSpace(h.Amount), 64)
		if err != nil {
			continue
		}
		matching = append(matching, h)
		total += avail
	}

	if len(matching) == 0 {
		return nil, nil, fmt.Errorf("no unlocked %s holdings found", asset)
	}

	// Sort largest first so the single-holding fast path picks the best fit.
	sort.Slice(matching, func(i, j int) bool {
		ai, errI := strconv.ParseFloat(strings.TrimSpace(matching[i].Amount), 64)
		aj, errJ := strconv.ParseFloat(strings.TrimSpace(matching[j].Amount), 64)
		if errI != nil || errJ != nil {
			return errJ != nil // push unparseable amounts to the end
		}
		return ai > aj
	})

	// Fast path: a single holding covers the requirement.
	if avail, err := strconv.ParseFloat(strings.TrimSpace(matching[0].Amount), 64); err == nil && avail >= required {
		h := matching[0]
		return &h, nil, nil
	}

	// Merge path: total is enough but no single holding is.
	if total >= required {
		return nil, matching, nil
	}

	return nil, nil, fmt.Errorf("insufficient %s balance: have %.10f, need %s", asset, total, amount)
}

// selectHoldingForLeg wraps checkMergeNeeded for callers that only want the single-holding path.
// Used by tests and the normal (non-merge) execution path.
func selectHoldingForLeg(holdings []wallet.HoldingContract, asset, amount string) (*wallet.HoldingContract, error) {
	single, _, err := checkMergeNeeded(holdings, asset, amount)
	return single, err
}
