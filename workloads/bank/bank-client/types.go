package main

import "time"

// These mirror the JSON shape served by bank-server's /api/summary endpoint.
// The two workloads are independently deployable, so the types are kept
// separate rather than shared.

type Account struct {
	HolderName    string `json:"holderName"`
	AccountNumber string `json:"accountNumber"`
	SortCode      string `json:"sortCode"`
}

type Transaction struct {
	Timestamp   time.Time `json:"timestamp"`
	Merchant    string    `json:"merchant"`
	Category    string    `json:"category"`
	AmountPence int64     `json:"amountPence"`
}

type Summary struct {
	Account      Account       `json:"account"`
	BalancePence int64         `json:"balancePence"`
	Transactions []Transaction `json:"transactions"`
}
