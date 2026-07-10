package main

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
)

type dashboardView struct {
	AuthMode     string
	Account      Account
	BalanceGBP   string
	Transactions []transactionView
	Error        string
	LoggedIn     bool
	UserName     string
	ChatEnabled  bool
}

type transactionView struct {
	Date      string
	Merchant  string
	Category  string
	AmountGBP string
	Positive  bool
}

func handleDashboard(fetcher *summaryFetcher, tmpl *template.Template, authMode string, sessions *sessionStore, chatEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var loggedIn bool
		var userName string
		if chatEnabled {
			sess, err := sessions.fromRequest(r)
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			loggedIn = true
			userName = sess.Name
			if userName == "" {
				userName = sess.Subject
			}
		}

		summary, err := fetcher.fetch(r.Context())
		if err != nil {
			slog.Error("Failed to fetch summary from bank-server", "error", err)
			w.WriteHeader(http.StatusBadGateway)
			if execErr := tmpl.Execute(w, dashboardView{AuthMode: authMode, Error: "Unable to reach bank-server. Please try again.", LoggedIn: loggedIn, UserName: userName, ChatEnabled: chatEnabled}); execErr != nil {
				slog.Error("Error rendering dashboard error state", "error", execErr)
			}
			return
		}

		view := dashboardView{
			AuthMode:    authMode,
			Account:     summary.Account,
			BalanceGBP:  formatMoney(summary.BalancePence),
			LoggedIn:    loggedIn,
			UserName:    userName,
			ChatEnabled: chatEnabled,
		}
		for i := len(summary.Transactions) - 1; i >= 0; i-- {
			txn := summary.Transactions[i]
			view.Transactions = append(view.Transactions, transactionView{
				Date:      txn.Timestamp.Format("2 Jan 2006"),
				Merchant:  txn.Merchant,
				Category:  txn.Category,
				AmountGBP: formatSignedMoney(txn.AmountPence),
				Positive:  txn.AmountPence > 0,
			})
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, view); err != nil {
			slog.Error("Error rendering dashboard", "error", err)
		}
	}
}

func formatMoney(pence int64) string {
	negative := pence < 0
	if negative {
		pence = -pence
	}
	s := fmt.Sprintf("£%d.%02d", pence/100, pence%100)
	if negative {
		return "-" + s
	}
	return s
}

func formatSignedMoney(pence int64) string {
	if pence >= 0 {
		return "+" + formatMoney(pence)
	}
	return formatMoney(pence)
}
