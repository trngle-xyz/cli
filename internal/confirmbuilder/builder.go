package confirmbuilder

import (
	"fmt"
	"strings"
	"time"

	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/wallet"
)

type BuildResult struct {
	Payload       wallet.CommandPayload
	TransferLegID string
}

// FactoryContext holds the allocation factory contract ID and optional
// context data returned by the Canton registry.  When non-nil, FactoryID
// overrides the operator-supplied FactoryCID and ChoiceContextData is used
// as extraArgs.context (matching the TypeScript SDK's behaviour).
type FactoryContext struct {
	FactoryID          string
	ExpectedAdmin      string // overrides holding.InstrumentAdmin when set (e.g. domain migration)
	ChoiceContextData  any
	DisclosedContracts []any
}

const allocationInterfaceID = "#splice-api-token-allocation-v1:Splice.Api.Token.AllocationV1:Allocation"

func BuildTakerConfirmPayload(ctx core.AcceptContext, holding wallet.HoldingContract, factoryCtx *FactoryContext) (BuildResult, error) {
	if ctx.QuoteID == "" || ctx.TradeCID == "" {
		return BuildResult{}, fmt.Errorf("invalid accept context: missing quote_id or trade_cid")
	}

	// Resolve the factory contract ID: prefer registry-resolved, fall back to operator-provided.
	factoryID := strings.TrimSpace(ctx.FactoryCID)
	if factoryCtx != nil && strings.TrimSpace(factoryCtx.FactoryID) != "" {
		factoryID = factoryCtx.FactoryID
	}
	if factoryID == "" {
		return BuildResult{}, fmt.Errorf("invalid accept context: allocation factory not resolved (missing allocation_factory_cid)")
	}

	if strings.TrimSpace(ctx.TakerParty) == "" || strings.TrimSpace(ctx.MakerParty) == "" || strings.TrimSpace(ctx.OperatorParty) == "" {
		return BuildResult{}, fmt.Errorf("invalid accept context: missing required party fields")
	}
	if strings.TrimSpace(ctx.TakerLeg.Amount) == "" {
		return BuildResult{}, fmt.Errorf("invalid accept context: missing taker leg amount")
	}
	now := time.Now().UTC()
	if !ctx.ConfirmBefore.IsZero() && now.After(ctx.ConfirmBefore.UTC()) {
		return BuildResult{}, fmt.Errorf("quote can no longer be confirmed (confirm_before reached)")
	}
	if strings.TrimSpace(holding.ContractID) == "" {
		return BuildResult{}, fmt.Errorf("holding contract is required")
	}
	if strings.TrimSpace(holding.InstrumentAdmin) == "" || strings.TrimSpace(holding.InstrumentID) == "" {
		return BuildResult{}, fmt.Errorf("holding instrument fields are required")
	}

	transferLegID := fmt.Sprintf("leg-%s-%s-%s", ctx.TradeID, ctx.TakerParty, ctx.MakerParty)
	if ctx.TradeID == "" {
		transferLegID = fmt.Sprintf("leg-%s", ctx.QuoteID)
	}

	// extraArgs.context: use registry-returned choiceContextData if available, else empty.
	var extraArgsContext any = map[string]any{"values": map[string]any{}}
	if factoryCtx != nil && factoryCtx.ChoiceContextData != nil {
		extraArgsContext = factoryCtx.ChoiceContextData
	}

	// Use the factory-provided admin when available (e.g. after testnet domain migration,
	// prepareTransfer returns the correct DSO::f22a while holdings still reference DSO::b143).
	allocAdmin := holding.InstrumentAdmin
	if factoryCtx != nil && strings.TrimSpace(factoryCtx.ExpectedAdmin) != "" {
		allocAdmin = factoryCtx.ExpectedAdmin
	}

	allocCmd := map[string]any{
		"ExerciseCommand": map[string]any{
			"templateId": "#splice-api-token-allocation-instruction-v1:Splice.Api.Token.AllocationInstructionV1:AllocationFactory",
			"contractId": factoryID,
			"choice":     "AllocationFactory_Allocate",
			"choiceArgument": map[string]any{
				"expectedAdmin": allocAdmin,
				"allocation": map[string]any{
					"settlement": map[string]any{
						"executor": ctx.OperatorParty,
						"settlementRef": map[string]any{
							"id":  fmt.Sprintf("settlement-%s", ctx.TradeID),
							"cid": nil,
						},
						"requestedAt":    now.Format(time.RFC3339),
						"allocateBefore": now.Add(5 * time.Minute).Format(time.RFC3339),
						"settleBefore":   now.Add(24 * time.Hour).Format(time.RFC3339),
						"meta": map[string]any{
							"values": map[string]any{},
						},
					},
					"transferLegId": transferLegID,
					"transferLeg": map[string]any{
						"sender":   ctx.TakerParty,
						"receiver": ctx.MakerParty,
						"amount":   ctx.TakerLeg.Amount,
						"instrumentId": map[string]any{
							"admin": allocAdmin,
							"id":    holding.InstrumentID,
						},
						"meta": map[string]any{
							"values": map[string]any{},
						},
					},
				},
				"requestedAt":      now.Format(time.RFC3339),
				"inputHoldingCids": []string{holding.ContractID},
				"extraArgs": map[string]any{
					"context": extraArgsContext,
					"meta": map[string]any{
						"values": map[string]any{},
					},
				},
			},
		},
	}

	tradeTemplateID := strings.TrimSpace(ctx.TradeTemplateID)
	if tradeTemplateID == "" {
		tradeTemplateID = "#trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2"
	} else if !strings.HasPrefix(tradeTemplateID, "#") {
		tradeTemplateID = "#" + tradeTemplateID
	}

	confirmCmd := map[string]any{
		"ExerciseCommand": map[string]any{
			"templateId": tradeTemplateID,
			"contractId": ctx.TradeCID,
			"choice":     "TakerConfirm",
			"choiceArgument": map[string]any{
				"allocationCid":   nil,
				"holdingRef":      holding.ContractID,
				"instrumentAdmin": allocAdmin,
			},
		},
	}

	// Build the disclosed contracts list. When the factory provides its own disclosed
	// contracts (e.g. from prepareTransfer), use those INSTEAD of the operator-provided
	// ones — the operator's may be stale after a domain migration (b143 → f22a).
	var disclosed []any
	if factoryCtx != nil && len(factoryCtx.DisclosedContracts) > 0 {
		disclosed = append(disclosed, factoryCtx.DisclosedContracts...)
	} else {
		disclosed = append(disclosed, ctx.Disclosed...)
	}
	// Disclose the holding if the created-event blob is present (matches TypeScript SDK).
	if strings.TrimSpace(holding.CreatedEventBlob) != "" && strings.TrimSpace(holding.SynchronizerID) != "" {
		disclosed = append(disclosed, map[string]any{
			"templateId":       holding.TemplateID,
			"contractId":       holding.ContractID,
			"createdEventBlob": holding.CreatedEventBlob,
			"synchronizerId":   holding.SynchronizerID,
		})
	}
	disclosed = deduplicateDisclosed(disclosed)

	payload := wallet.CommandPayload{
		Commands:                     []any{allocCmd, confirmCmd},
		DisclosedContracts:           disclosed,
		ActAs:                        []string{ctx.TakerParty},
		ReadAs:                       []string{ctx.TakerParty},
		SynchronizerID:               holding.SynchronizerID,
		PackageIDSelectionPreference: []string{},
	}

	return BuildResult{Payload: payload, TransferLegID: transferLegID}, nil
}

func ParseConfirmResult(tx any) (tradeCID string, allocationID string) {
	m, ok := tx.(map[string]any)
	if !ok {
		return "", ""
	}

	if v, ok := m["trade_cid_after_taker_confirm"].(string); ok && strings.TrimSpace(v) != "" {
		tradeCID = v
	}
	if v, ok := m["allocation_id"].(string); ok && strings.TrimSpace(v) != "" {
		allocationID = v
	}

	events := extractEventsByID(m)
	if len(events) == 0 {
		return tradeCID, allocationID
	}

	if tradeCID == "" {
		for _, e := range events {
			created := extractCreatedEvent(e)
			if created == nil {
				continue
			}
			templateID, _ := created["templateId"].(string)
			contractID, _ := created["contractId"].(string)
			if strings.HasSuffix(templateID, ":TradeConfirmV2") && strings.TrimSpace(contractID) != "" {
				tradeCID = contractID
				break
			}
		}
	}

	if allocationID == "" {
		for _, e := range events {
			ex := extractExercisedEvent(e)
			if ex == nil {
				continue
			}
			choice, _ := ex["choice"].(string)
			if choice != "AllocationFactory_Allocate" {
				continue
			}
			if r, ok := ex["exerciseResult"].(map[string]any); ok {
				if v, ok := r["allocationCid"].(string); ok && strings.TrimSpace(v) != "" {
					allocationID = v
					break
				}
				if output, ok := r["output"].(map[string]any); ok {
					if v, ok := output["allocationCid"].(string); ok && strings.TrimSpace(v) != "" {
						allocationID = v
						break
					}
					if nested, ok := output["value"].(map[string]any); ok {
						if v, ok := nested["allocationCid"].(string); ok && strings.TrimSpace(v) != "" {
							allocationID = v
							break
						}
					}
				}
			}
		}
	}

	if allocationID == "" {
		for _, e := range events {
			created := extractCreatedEvent(e)
			if created == nil {
				continue
			}
			templateID, _ := created["templateId"].(string)
			contractID, _ := created["contractId"].(string)
			if strings.TrimSpace(contractID) == "" {
				continue
			}
			if strings.HasSuffix(templateID, ":Allocation") || templateID == allocationInterfaceID {
				allocationID = contractID
				break
			}
		}
	}

	return tradeCID, allocationID
}

func extractEventsByID(root map[string]any) map[string]any {
	if txTree, ok := root["transactionTree"].(map[string]any); ok {
		if events, ok := txTree["eventsById"].(map[string]any); ok {
			return events
		}
	}
	if events, ok := root["eventsById"].(map[string]any); ok {
		return events
	}
	return nil
}

func extractCreatedEvent(event any) map[string]any {
	m, ok := event.(map[string]any)
	if !ok {
		return nil
	}
	if v, ok := m["CreatedTreeEvent"].(map[string]any); ok {
		if value, ok := v["value"].(map[string]any); ok {
			return value
		}
		return v
	}
	if v, ok := m["created"].(map[string]any); ok {
		return v
	}
	return nil
}

func extractExercisedEvent(event any) map[string]any {
	m, ok := event.(map[string]any)
	if !ok {
		return nil
	}
	if v, ok := m["ExercisedTreeEvent"].(map[string]any); ok {
		if value, ok := v["value"].(map[string]any); ok {
			return value
		}
		return v
	}
	if v, ok := m["exercised"].(map[string]any); ok {
		return v
	}
	return nil
}

// deduplicateDisclosed removes duplicate disclosed contracts by contractId.
func deduplicateDisclosed(contracts []any) []any {
	seen := make(map[string]bool)
	out := make([]any, 0, len(contracts))
	for _, c := range contracts {
		m, ok := c.(map[string]any)
		if !ok {
			out = append(out, c)
			continue
		}
		cid, _ := m["contractId"].(string)
		if cid == "" || !seen[cid] {
			if cid != "" {
				seen[cid] = true
			}
			out = append(out, c)
		}
	}
	return out
}
