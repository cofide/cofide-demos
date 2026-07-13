package main

import (
	"sync"
	"time"
)

type Account struct {
	HolderName    string `json:"holderName"`
	AccountNumber string `json:"accountNumber"`
	SortCode      string `json:"sortCode"`
}

type Transaction struct {
	ID             int       `json:"id"`
	Timestamp      time.Time `json:"timestamp"`
	Merchant       string    `json:"merchant"`
	Category       string    `json:"category"`
	AmountPence    int64     `json:"amountPence"`
	FraudChecked   bool      `json:"fraudChecked"`
	FraudCheckedAt time.Time `json:"fraudCheckedAt,omitzero"`
}

type Summary struct {
	Account      Account       `json:"account"`
	BalancePence int64         `json:"balancePence"`
	Transactions []Transaction `json:"transactions"`
}

// Ledger is an in-memory, mutex-guarded account balance and transaction
// history. State resets on restart — there is no persistence, which is fine
// for demo purposes.
type Ledger struct {
	mu           sync.Mutex
	account      Account
	balancePence int64
	transactions []Transaction
	nextID       int
}

func newLedger() *Ledger {
	seedTime := time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC)
	l := &Ledger{
		account: Account{
			HolderName:    "Alex Morgan",
			AccountNumber: "12345678",
			SortCode:      "12-34-56",
		},
		balancePence: 284217,
		transactions: []Transaction{
			{Timestamp: seedTime.Add(-6 * 24 * time.Hour), Merchant: "Waitrose", Category: "Groceries", AmountPence: -4521},
			{Timestamp: seedTime.Add(-5 * 24 * time.Hour), Merchant: "Transport for London", Category: "Transport", AmountPence: -680},
			{Timestamp: seedTime.Add(-4 * 24 * time.Hour), Merchant: "Acme Corp", Category: "Salary", AmountPence: 320000},
			{Timestamp: seedTime.Add(-3 * 24 * time.Hour), Merchant: "Netflix", Category: "Entertainment", AmountPence: -1299},
			{Timestamp: seedTime.Add(-2 * 24 * time.Hour), Merchant: "Pret A Manger", Category: "Dining", AmountPence: -595},
			{Timestamp: seedTime.Add(-1 * 24 * time.Hour), Merchant: "British Gas", Category: "Utilities", AmountPence: -8734},
		},
	}
	for i := range l.transactions {
		l.nextID++
		l.transactions[i].ID = l.nextID
	}
	return l
}

func (l *Ledger) Summary() Summary {
	l.mu.Lock()
	defer l.mu.Unlock()

	transactions := make([]Transaction, len(l.transactions))
	copy(transactions, l.transactions)

	return Summary{
		Account:      l.account,
		BalancePence: l.balancePence,
		Transactions: transactions,
	}
}

func (l *Ledger) AddTransaction(txn Transaction) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if txn.Timestamp.IsZero() {
		txn.Timestamp = time.Now()
	}
	l.nextID++
	txn.ID = l.nextID
	l.transactions = append(l.transactions, txn)
	l.balancePence += txn.AmountPence
}

// MarkFraudChecked marks every transaction not yet checked as checked at the
// given time, and returns how many were updated. Callers (bank-fraud-checker)
// don't enumerate specific transaction IDs — every poll simply clears
// whatever's currently pending.
func (l *Ledger) MarkFraudChecked(at time.Time) (count int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for i := range l.transactions {
		if !l.transactions[i].FraudChecked {
			l.transactions[i].FraudChecked = true
			l.transactions[i].FraudCheckedAt = at
			count++
		}
	}
	return count
}
