package wallet

type WalletAdapter interface {
	PartyID() string
	PublicKey() string

	Initialize(privateKeyHex string) error

	GetBalances() ([]Balance, error)
	GetHoldingContracts(interfaceID string) ([]HoldingContract, error)

	PrepareAndSubmit(payload CommandPayload) (*TransactionResult, error)

	GetPendingTransfers() ([]PendingTransfer, error)
	AcceptTransfer(transferID string) (*TransactionResult, error)
}

type Balance struct {
	Symbol          string `json:"symbol"`
	InstrumentID    string `json:"instrument_id"`
	InstrumentAdmin string `json:"instrument_admin"`
	Amount          string `json:"amount"`
}

type HoldingContract struct {
	ContractID       string `json:"contract_id"`
	TemplateID       string `json:"template_id"`
	Amount           string `json:"amount"`
	InstrumentID     string `json:"instrument_id"`
	InstrumentAdmin  string `json:"instrument_admin"`
	CreatedEventBlob string `json:"created_event_blob"`
	SynchronizerID   string `json:"synchronizer_id"`
	IsLocked         bool   `json:"is_locked"`
}

type CommandPayload struct {
	Commands                     []any    `json:"commands"`
	DisclosedContracts           []any    `json:"disclosed_contracts"`
	ActAs                        []string `json:"act_as"`
	ReadAs                       []string `json:"read_as"`
	SynchronizerID               string   `json:"synchronizer_id"`
	PackageIDSelectionPreference []string `json:"package_id_selection_preference"`
}

type TransactionResult struct {
	CommandID       string `json:"command_id"`
	TransactionTree any    `json:"transaction_tree"`
}

type PendingTransfer struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	Amount    string `json:"amount"`
	Asset     string `json:"asset"`
	ExpiresAt string `json:"expires_at"`
}

