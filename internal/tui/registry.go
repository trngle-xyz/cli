package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/wallet"
	"github.com/trngle-xyz/cli/internal/wallet/loop"
)

// Network-specific constants, scan registries, and utility token contract
// data are defined in network_data.go.

type registryFactoryResult struct {
	FactoryID          string
	ExpectedAdmin      string // overrides holding.InstrumentAdmin when set (e.g. after domain migration)
	ChoiceContextData  any
	DisclosedContracts []any
}

// fetchRegistryFactoryFn is a package-level var so tests can stub it out.
var fetchRegistryFactoryFn = defaultFetchRegistryFactory

// fetchFactoryViaPrepareTransfer calls the Loop API's prepareTransfer endpoint
// to discover the correct AllocationFactory for CC/Amulet. On testnet, the scan
// registry returns stale contracts from the old domain, but prepareTransfer
// returns the correct ExternalPartyAmuletRules (which implements both
// TransferFactory and AllocationFactory). See ALLOCATION_FIX.md for details.
func fetchFactoryViaPrepareTransfer(authToken string, holding wallet.HoldingContract, network string) (*registryFactoryResult, error) {
	apiURL, ok := loop.NetworkAPIURLs[strings.ToLower(network)]
	if !ok || authToken == "" {
		return nil, fmt.Errorf("no API URL for network %s or no auth token", network)
	}

	now := time.Now().UTC()
	reqBody := map[string]any{
		"recipient":      holding.InstrumentAdmin, // dummy recipient (we won't execute)
		"amount":         "0.0001",
		"instrument_id":  holding.InstrumentID,
		"requested_at":   now.Format(time.RFC3339),
		"execute_before": now.Add(5 * time.Minute).Format(time.RFC3339),
	}
	// If admin is empty (Amulet), use instrument_id only
	if strings.TrimSpace(holding.InstrumentAdmin) != "" {
		reqBody["instrument"] = map[string]any{
			"instrument_admin": holding.InstrumentAdmin,
			"instrument_id":    holding.InstrumentID,
		}
	}

	enc, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("prepareTransfer: marshal: %w", err)
	}
	req, err := http.NewRequest("POST", apiURL+"/api/v1/.connect/pair/transfer", bytes.NewReader(enc))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prepareTransfer: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prepareTransfer: http %d", resp.StatusCode)
	}

	// Parse the response to extract factory + context + disclosed
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("prepareTransfer: parse: %w", err)
	}

	// The response may be wrapped in { payload: { commands, disclosedContracts } }
	// or directly { commands, disclosedContracts }
	payload, _ := raw["payload"].(map[string]any)
	if payload == nil {
		payload = raw
	}

	commands, _ := payload["commands"].([]any)
	if len(commands) == 0 {
		return nil, fmt.Errorf("prepareTransfer: no commands in response")
	}

	// Extract the ExerciseCommand from the first command
	cmd0, _ := commands[0].(map[string]any)
	exerciseCmd, _ := cmd0["ExerciseCommand"].(map[string]any)
	if exerciseCmd == nil {
		return nil, fmt.Errorf("prepareTransfer: no ExerciseCommand in first command")
	}

	factoryID, _ := exerciseCmd["contractId"].(string)
	if factoryID == "" {
		return nil, fmt.Errorf("prepareTransfer: no contractId")
	}

	// Extract choiceArgument.extraArgs.context
	choiceArg, _ := exerciseCmd["choiceArgument"].(map[string]any)
	extraArgs, _ := choiceArg["extraArgs"].(map[string]any)
	contextData, _ := extraArgs["context"].(map[string]any)

	// Extract expectedAdmin — on testnet after domain migration, prepareTransfer
	// returns the correct admin (f22a) while holdings still reference the old (b143).
	expectedAdmin, _ := choiceArg["expectedAdmin"].(string)

	// Extract disclosed contracts (filtering out the user's holding)
	disclosedRaw, _ := payload["disclosedContracts"].([]any)
	var disclosed []any
	for _, d := range disclosedRaw {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		tid, _ := dm["templateId"].(string)
		// Exclude the user's own holding (Amulet/LockedAmulet) — only keep system contracts
		if strings.Contains(tid, ":Amulet:Amulet") || strings.Contains(tid, ":Amulet:LockedAmulet") {
			continue
		}
		disclosed = append(disclosed, d)
	}

	return &registryFactoryResult{
		FactoryID:          factoryID,
		ExpectedAdmin:      expectedAdmin,
		ChoiceContextData:  contextData,
		DisclosedContracts: disclosed,
	}, nil
}

// defaultFetchRegistryFactory calls the Canton scan registry to resolve the
// AllocationFactory contract, mirroring what the TypeScript SDK does in
// fetchFactory().  Returns the factory CID, choiceContextData, and disclosed
// contracts needed to submit the AllocationFactory_Allocate command.
// The network parameter ("mainnet"/"testnet") determines which admin mapping
// and registry URLs to use.
func defaultFetchRegistryFactory(authToken string, holding wallet.HoldingContract, ctx *core.AcceptContext, transferLegID string, network string) (*registryFactoryResult, error) {
	// For testnet utility tokens, use hardcoded contracts (scan registry returns empty context)
	if hardcoded := buildHardcodedUtilityFactory(holding.InstrumentAdmin, network); hardcoded != nil {
		return hardcoded, nil
	}

	// For CC/Amulet on testnet, use prepareTransfer to get the correct factory
	// (the scan registry returns stale contracts from the old domain).
	if strings.EqualFold(network, "testnet") && !isUtilitiesAdmin(holding.InstrumentAdmin, network) {
		if result, err := fetchFactoryViaPrepareTransfer(authToken, holding, network); err == nil {
			return result, nil
		}
		// Fall through to scan registry if prepareTransfer fails
	}

	now := time.Now().UTC()

	reqBody := map[string]any{
		"choiceArguments": map[string]any{
			"expectedAdmin": holding.InstrumentAdmin,
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
					"meta":           map[string]any{"values": map[string]any{}},
				},
				"transferLegId": transferLegID,
				"transferLeg": map[string]any{
					"sender":   ctx.TakerParty,
					"receiver": ctx.MakerParty,
					"amount":   ctx.TakerLeg.Amount,
					"instrumentId": map[string]any{
						"admin": holding.InstrumentAdmin,
						"id":    holding.InstrumentID,
					},
					"meta": map[string]any{"values": map[string]any{}},
				},
			},
			"requestedAt":      now.Format(time.RFC3339),
			"inputHoldingCids": []string{holding.ContractID},
			"extraArgs": map[string]any{
				"context": map[string]any{"values": map[string]any{}},
				"meta":    map[string]any{"values": map[string]any{}},
			},
		},
	}

	var registryURLs []string
	if isUtilitiesAdmin(holding.InstrumentAdmin, network) {
		registryURLs = []string{
			fmt.Sprintf("%s/api/token-standard/v0/registrars/%s/registry/allocation-instruction/v1/allocation-factory",
				utilitiesBaseURLForNetwork(network), url.PathEscape(holding.InstrumentAdmin)),
		}
	} else {
		for _, base := range scanRegistriesForNetwork(network) {
			registryURLs = append(registryURLs, base+"/registry/allocation-instruction/v1/allocation-factory")
		}
	}

	enc, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("registryFactory: marshal: %w", err)
	}
	httpClient := &http.Client{Timeout: 12 * time.Second}

	for _, registryURL := range registryURLs {
		req, err := http.NewRequest(http.MethodPost, registryURL, bytes.NewReader(enc))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		var out struct {
			FactoryID     string `json:"factoryId"`
			ChoiceContext struct {
				DisclosedContracts []any `json:"disclosedContracts"`
				ChoiceContextData  any   `json:"choiceContextData"`
			} `json:"choiceContext"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			continue
		}
		if strings.TrimSpace(out.FactoryID) == "" {
			continue
		}
		return &registryFactoryResult{
			FactoryID:          out.FactoryID,
			ChoiceContextData:  out.ChoiceContext.ChoiceContextData,
			DisclosedContracts: out.ChoiceContext.DisclosedContracts,
		}, nil
	}

	return nil, fmt.Errorf("allocation factory not found in registry for admin=%s", holding.InstrumentAdmin)
}

// ── TransferFactory (merge / self-transfer) ────────────────────────────────

const transferFactoryTemplateID = "#splice-api-token-transfer-instruction-v1:Splice.Api.Token.TransferInstructionV1:TransferFactory"

type transferFactoryResult struct {
	FactoryID          string
	ChoiceContextData  any
	DisclosedContracts []any
}

// fetchTransferFactory calls the Splice registry to get a TransferFactory
// contract for a self-transfer merge. Pattern mirrors defaultFetchRegistryFactory.
func fetchTransferFactory(authToken string, holdings []wallet.HoldingContract, takerParty string, total float64, network string) (*transferFactoryResult, error) {
	if len(holdings) == 0 {
		return nil, fmt.Errorf("no holdings provided for merge")
	}
	primary := holdings[0]
	now := time.Now().UTC()

	cids := make([]string, len(holdings))
	for i, h := range holdings {
		cids[i] = h.ContractID
	}

	reqBody := map[string]any{
		"choiceArguments": map[string]any{
			"expectedAdmin": primary.InstrumentAdmin,
			"transfer": map[string]any{
				"sender":   takerParty,
				"receiver": takerParty,
				"amount":   fmt.Sprintf("%.10f", total),
				"instrumentId": map[string]any{
					"admin": primary.InstrumentAdmin,
					"id":    primary.InstrumentID,
				},
				"lock":             nil,
				"requestedAt":      now.Format(time.RFC3339),
				"executeBefore":    now.Add(24 * time.Hour).Format(time.RFC3339),
				"meta":             map[string]any{"values": map[string]any{}},
				"inputHoldingCids": cids,
			},
			"extraArgs": map[string]any{
				"context": map[string]any{"values": map[string]any{}},
				"meta":    map[string]any{"values": map[string]any{}},
			},
		},
	}

	var registryURLs []string
	if isUtilitiesAdmin(primary.InstrumentAdmin, network) {
		registryURLs = []string{
			fmt.Sprintf("%s/api/token-standard/v0/registrars/%s/registry/transfer-instruction/v1/transfer-factory",
				utilitiesBaseURLForNetwork(network), url.PathEscape(primary.InstrumentAdmin)),
		}
	} else {
		for _, base := range scanRegistriesForNetwork(network) {
			registryURLs = append(registryURLs, base+"/registry/transfer-instruction/v1/transfer-factory")
		}
	}

	enc, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("transferFactory: marshal: %w", err)
	}
	httpClient := &http.Client{Timeout: 12 * time.Second}
	var lastErr string
	for _, registryURL := range registryURLs {
		req, err := http.NewRequest(http.MethodPost, registryURL, bytes.NewReader(enc))
		if err != nil {
			lastErr = err.Error()
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err.Error()
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			bodyStr := string(body)
			if len(bodyStr) > 150 {
				bodyStr = bodyStr[:150]
			}
			lastErr = fmt.Sprintf("registry %d: %s", resp.StatusCode, bodyStr)
			continue
		}
		var out struct {
			FactoryID     string `json:"factoryId"`
			ChoiceContext struct {
				DisclosedContracts []any `json:"disclosedContracts"`
				ChoiceContextData  any   `json:"choiceContextData"`
			} `json:"choiceContext"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			lastErr = fmt.Sprintf("parse: %s", err.Error())
			continue
		}
		if strings.TrimSpace(out.FactoryID) == "" {
			lastErr = "empty factory ID in response"
			continue
		}
		return &transferFactoryResult{
			FactoryID:          out.FactoryID,
			ChoiceContextData:  out.ChoiceContext.ChoiceContextData,
			DisclosedContracts: out.ChoiceContext.DisclosedContracts,
		}, nil
	}
	if lastErr != "" {
		return nil, fmt.Errorf("transfer factory: %s", lastErr)
	}
	return nil, fmt.Errorf("transfer factory not found in registry for admin=%s", primary.InstrumentAdmin)
}
