package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/trngle-xyz/cli/internal/confirmbuilder"
	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/wallet"
	"github.com/trngle-xyz/cli/internal/wallet/loop"
)

// executeAcceptContextSubmit builds and submits the allocation + taker-confirm commands.
// If preSelected is non-nil it is used directly, skipping holding fetch and selection.
func executeAcceptContextSubmit(client core.QuoteClient, adapter tradeWallet, activeQuote *ActiveQuote, preSelected *wallet.HoldingContract, network string) quoteSubmitResultMsg {
	if activeQuote.AcceptContext == nil {
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("missing accept context")}
	}
	ctx := activeQuote.AcceptContext

	// Pay any pending gas BEFORE fetching holdings. Gas payment consumes CC
	// holdings and creates new ones, so we must settle gas first to ensure
	// the holding CIDs we fetch are still valid when we submit the allocation.
	if gp, ok := adapter.(interface{ EnsureGasPaid() error }); ok {
		_ = gp.EnsureGasPaid()
	}

	var holding *wallet.HoldingContract
	if preSelected != nil {
		// Pre-selected holding may be stale after gas payment — re-fetch.
		holdings, err := adapter.GetHoldingContracts("")
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("fetch holdings: %w", err)}
		}
		holding, err = selectHoldingForLeg(holdings, ctx.TakerLeg.Asset, ctx.TakerLeg.Amount)
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
		}
	} else {
		holdings, err := adapter.GetHoldingContracts("")
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("fetch holdings: %w", err)}
		}
		holding, err = selectHoldingForLeg(holdings, ctx.TakerLeg.Asset, ctx.TakerLeg.Amount)
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
		}
	}

	// Compute transfer leg ID (must match what the builder uses).
	transferLegID := fmt.Sprintf("leg-%s-%s-%s", ctx.TradeID, ctx.TakerParty, ctx.MakerParty)
	if ctx.TradeID == "" {
		transferLegID = fmt.Sprintf("leg-%s", ctx.QuoteID)
	}

	// Resolve the AllocationFactory via the Canton registry (mirrors the TypeScript SDK).
	// Get the auth token from the adapter if it exposes one.
	authToken := ""
	if tokenHolder, ok := adapter.(interface{ AuthToken() string }); ok {
		authToken = tokenHolder.AuthToken()
	}

	var factoryCtx *confirmbuilder.FactoryContext
	if result, registryErr := fetchRegistryFactoryFn(authToken, *holding, ctx, transferLegID, network); registryErr == nil {
		factoryCtx = &confirmbuilder.FactoryContext{
			FactoryID:          result.FactoryID,
			ExpectedAdmin:      result.ExpectedAdmin,
			ChoiceContextData:  result.ChoiceContextData,
			DisclosedContracts: result.DisclosedContracts,
		}
	} else if strings.TrimSpace(ctx.FactoryCID) != "" {
		// Registry unavailable — fall back to the operator-provided factory CID.
		// The allocation will proceed without registry-sourced disclosed contracts
		// and choiceContextData, which may or may not work depending on the asset.
		factoryCtx = &confirmbuilder.FactoryContext{
			FactoryID: ctx.FactoryCID,
		}
	} else {
		return quoteSubmitResultMsg{
			quoteID:   activeQuote.ID,
			submitErr: fmt.Errorf("allocation factory unavailable (registry: %w; operator provided none)", registryErr),
		}
	}

	build, err := confirmbuilder.BuildTakerConfirmPayload(*ctx, *holding, factoryCtx)
	if err != nil {
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
	}
	if len(build.Payload.Commands) < 2 {
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("invalid confirm payload: expected allocate + confirm commands")}
	}

	allocatePayload := build.Payload
	allocatePayload.Commands = []any{build.Payload.Commands[0]}
	allocatePayload.SynchronizerID = ""

	// Helper to report errors to the operator for immediate cleanup.
	// Best-effort: error reporting failure should not block the trade flow.
	reportErr := func(phase, allocID string, submitErr error) {
		_ = client.ReportError(context.Background(), activeQuote.ID, core.ErrorReport{
			AcceptContextID: ctx.AcceptContextID,
			Phase:           phase,
			ErrorMessage:    submitErr.Error(),
			AllocationID:    allocID,
		})
	}

	// Allocate with retry: if the first attempt's allocation isn't found (Canton
	// silently rejects stale holding CIDs after gas payment), re-fetch holdings,
	// rebuild the payload, and try again.
	var allocationID string
	for allocAttempt := 0; allocAttempt < 2; allocAttempt++ {
		if allocAttempt > 0 {
			// Gas payment may have consumed the holding — re-fetch everything.
			if gp, ok := adapter.(interface{ EnsureGasPaid() error }); ok {
				_ = gp.EnsureGasPaid()
			}
			time.Sleep(1 * time.Second)
			freshHoldings, fetchErr := adapter.GetHoldingContracts("")
			if fetchErr != nil {
				reportErr("allocation", "", fetchErr)
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("retry fetch holdings: %w", fetchErr)}
			}
			freshHolding, selErr := selectHoldingForLeg(freshHoldings, ctx.TakerLeg.Asset, ctx.TakerLeg.Amount)
			if selErr != nil {
				reportErr("allocation", "", selErr)
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: selErr}
			}
			holding = freshHolding
			retryBuild, buildErr := confirmbuilder.BuildTakerConfirmPayload(*ctx, *holding, factoryCtx)
			if buildErr != nil {
				reportErr("allocation", "", buildErr)
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: buildErr}
			}
			build = retryBuild
			allocatePayload = build.Payload
			allocatePayload.Commands = []any{build.Payload.Commands[0]}
			allocatePayload.SynchronizerID = ""
		}

		// Snapshot allocations before allocate so we can diff to find the new one.
		allocsBefore, _ := adapter.GetHoldingContracts(allocationInterfaceID)
		allocCIDsBefore := make(map[string]bool, len(allocsBefore))
		for _, a := range allocsBefore {
			allocCIDsBefore[a.ContractID] = true
		}

		allocateTx, err := adapter.PrepareAndSubmit(allocatePayload)
		if err != nil {
			reportErr("allocation", "", err)
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
		}
		_, allocationID = confirmbuilder.ParseConfirmResult(allocateTx.TransactionTree)

		if strings.TrimSpace(allocationID) == "" {
			var pollDiag string
			allocationID, pollDiag = pollForNewAllocation(adapter, holding, allocCIDsBefore)
			if strings.TrimSpace(allocationID) == "" {
				if allocAttempt == 0 {
					continue // retry with fresh holdings
				}
				parseErr := fmt.Errorf("allocation submit succeeded but allocation CID not found (%s)", pollDiag)
				reportErr("allocation", "", parseErr)
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: parseErr}
			}
		}
		break
	}

	// Pay any gas incurred by the allocate transaction before submitting confirm.
	// On Loop testnet, gas is charged after each transaction and blocks the next.
	if gp, ok := adapter.(interface{ EnsureGasPaid() error }); ok {
		_ = gp.EnsureGasPaid()
	}

	confirmPayload := build.Payload
	confirmPayload.Commands = []any{build.Payload.Commands[1]}
	confirmPayload.SynchronizerID = ""
	if err := setConfirmAllocationCID(confirmPayload.Commands[0], allocationID, "allocationCid"); err != nil {
		reportErr("taker_confirm", allocationID, err)
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
	}

	confirmTx, err := adapter.PrepareAndSubmit(confirmPayload)
	if err != nil && strings.Contains(err.Error(), "AllocationCID") {
		if patchErr := setConfirmAllocationCID(confirmPayload.Commands[0], allocationID, "allocationCID"); patchErr == nil {
			confirmTx, err = adapter.PrepareAndSubmit(confirmPayload)
		}
	}
	if err != nil && isContractNotFoundErr(err) {
		currentTemplateID := extractConfirmTemplateID(confirmPayload.Commands[0])
		for _, candidate := range confirmTemplateCandidates(currentTemplateID) {
			if candidate == currentTemplateID {
				continue
			}
			if !setConfirmTemplateID(confirmPayload.Commands[0], candidate) {
				continue
			}
			confirmTx, err = adapter.PrepareAndSubmit(confirmPayload)
			if err == nil || !isContractNotFoundErr(err) {
				break
			}
		}
	}
	if err != nil {
		reportErr("taker_confirm", allocationID, err)
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
	}

	tradeCID, _ := confirmbuilder.ParseConfirmResult(confirmTx.TransactionTree)
	if tradeCID == "" {
		tradeCID = ctx.TradeCID
	}

	// If the taker allocated CC/Amulet, query the LockedAmulet blob and pass
	// it to the operator. The operator cannot see taker LockedAmulets (stakeholders
	// are [DSO, sender]) and needs the blob as a disclosed contract for settlement.
	confirmRequest := map[string]any{
		"accept_context_id": ctx.AcceptContextID,
	}
	if finder, ok := adapter.(interface {
		GetLockedAmuletForAllocation(string) (*loop.LockedAmuletBlob, error)
	}); ok && strings.TrimSpace(allocationID) != "" {
		if blob, err := finder.GetLockedAmuletForAllocation(allocationID); err == nil && blob != nil {
			confirmRequest["locked_amulet"] = map[string]any{
				"contract_id":        blob.ContractID,
				"template_id":        blob.TemplateID,
				"created_event_blob": blob.CreatedEventBlob,
				"synchronizer_id":    blob.SynchronizerID,
			}
		}
		// Non-fatal: CC/Amulet allocations need this, but utility token
		// allocations (CBTC, USDCx) don't have LockedAmulets.
	}
	confirmErr := client.ConfirmQuote(context.Background(), activeQuote.ID, confirmRequest)

	return quoteSubmitResultMsg{
		quoteID:      activeQuote.ID,
		tradeCID:     tradeCID,
		allocationID: allocationID,
		confirmErr:   confirmErr,
		submitErr:    nil,
	}
}

func setConfirmAllocationCID(cmd any, allocationID, key string) error {
	root, ok := cmd.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid confirm command payload: expected object")
	}
	exercise, ok := root["ExerciseCommand"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid confirm command payload: missing ExerciseCommand")
	}
	args, ok := exercise["choiceArgument"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid confirm command payload: missing choiceArgument")
	}
	delete(args, "allocationCid")
	delete(args, "allocationCID")
	args[key] = allocationID
	return nil
}

func isContractNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "contract_not_found") ||
		strings.Contains(msg, "contract not found") ||
		strings.Contains(msg, "package_names_not_found") ||
		strings.Contains(msg, "package names not found")
}

func setConfirmTemplateID(cmd any, templateID string) bool {
	root, ok := cmd.(map[string]any)
	if !ok {
		return false
	}
	exercise, ok := root["ExerciseCommand"].(map[string]any)
	if !ok {
		return false
	}
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return false
	}
	exercise["templateId"] = templateID
	return true
}

func extractConfirmTemplateID(cmd any) string {
	root, ok := cmd.(map[string]any)
	if !ok {
		return ""
	}
	exercise, ok := root["ExerciseCommand"].(map[string]any)
	if !ok {
		return ""
	}
	templateID, _ := exercise["templateId"].(string)
	return strings.TrimSpace(templateID)
}

func confirmTemplateCandidates(templateID string) []string {
	base := strings.TrimSpace(templateID)
	if base == "" {
		base = "#trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2"
	}
	noHash := strings.TrimPrefix(base, "#")

	candidates := []string{
		base,
		noHash,
		"#" + noHash,
	}

	if strings.HasSuffix(noHash, ":V2.TradeConfirmV2:TradeConfirmV2") {
		prefix := strings.TrimSuffix(noHash, ":V2.TradeConfirmV2:TradeConfirmV2")
		candidates = append(candidates,
			prefix+":TradeConfirmV2:TradeConfirmV2",
			"#"+prefix+":TradeConfirmV2:TradeConfirmV2",
		)
	}
	if strings.HasSuffix(noHash, ":TradeConfirmV2:TradeConfirmV2") {
		prefix := strings.TrimSuffix(noHash, ":TradeConfirmV2:TradeConfirmV2")
		candidates = append(candidates,
			prefix+":V2.TradeConfirmV2:TradeConfirmV2",
			"#"+prefix+":V2.TradeConfirmV2:TradeConfirmV2",
		)
	}

	if strings.HasPrefix(noHash, "trngle-cip56:") {
		replaced := strings.Replace(noHash, "trngle-cip56:", "trngle-cip56-v2:", 1)
		candidates = append(candidates, replaced, "#"+replaced)
	}
	if strings.HasPrefix(noHash, "trngle-cip56-v2:") {
		replaced := strings.Replace(noHash, "trngle-cip56-v2:", "trngle-cip56:", 1)
		candidates = append(candidates, replaced, "#"+replaced)
	}

	candidates = append(candidates,
		"#trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2",
		"trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2",
		"#trngle-cip56:TradeConfirmV2:TradeConfirmV2",
		"trngle-cip56:TradeConfirmV2:TradeConfirmV2",
		"#trngle-cip56:V2.TradeConfirmV2:TradeConfirmV2",
		"trngle-cip56:V2.TradeConfirmV2:TradeConfirmV2",
	)

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}
