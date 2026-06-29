package capital

import "time"

const (
	VersionV1 = "v1"
	VersionV2 = "v2"

	EventPaymentInitiated = "PAYMENT_INITIATED"
	EventPaymentApproved  = "PAYMENT_APPROVED"
	EventPaymentWritten   = "PAYMENT_WRITTEN"
	EventMTLSOK           = "MTLS_HANDSHAKE_OK"
	EventAttackSucceeded  = "ATTACK_SUCCEEDED"
	EventAttackRejected   = "ATTACK_REJECTED"
)

type Account struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Balance int64  `json:"balance"`
}

type PaymentRequest struct {
	PaymentID   string `json:"payment_id"`
	FromAccount string `json:"from_account"`
	ToAccount   string `json:"to_account"`
	Amount      int64  `json:"amount"`
	Reference   string `json:"reference"`
}

type PaymentResult struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type LedgerRecord struct {
	PaymentID   string    `json:"payment_id"`
	FromAccount string    `json:"from_account"`
	ToAccount   string    `json:"to_account"`
	Amount      int64     `json:"amount"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type Event struct {
	Timestamp   time.Time      `json:"ts"`
	Service     string         `json:"service"`
	EventType   string         `json:"event_type"`
	SrcSPIFFEID string         `json:"src_spiffe_id,omitempty"`
	DstSPIFFEID string         `json:"dst_spiffe_id,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

func DemoAccounts() []Account {
	return []Account{
		{ID: "ACC-0001", Name: "Alice Chen", Balance: 25000000},
		{ID: "ACC-0002", Name: "Bob Okafor", Balance: 17500000},
		{ID: "ACC-9999", Name: "External recipient", Balance: 0},
	}
}

func DemoPayment() PaymentRequest {
	return PaymentRequest{
		PaymentID:   NewPaymentID(),
		FromAccount: "ACC-0001",
		ToAccount:   "ACC-9999",
		Amount:      4750000,
		Reference:   "New beneficiary transfer",
	}
}
