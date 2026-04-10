package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ExchangeClient performs RFC 8693 token exchange requests against a token endpoint.
type ExchangeClient struct {
	tokenURL string
	client   *http.Client
}

// ExchangeParams holds the parameters for an RFC 8693 token exchange request.
type ExchangeParams struct {
	ClientAssertionType string
	ClientAssertion     string
	SubjectTokenType    string
	SubjectToken        string
	ActorToken          string
	ActorTokenType      string
	Audience            string
	Scopes              []string
}

// ExchangeResult holds the access token returned by a successful token exchange.
type ExchangeResult struct {
	Token string
}

// Exchange performs an RFC 8693 token exchange and returns the resulting access token.
func (c *ExchangeClient) Exchange(ctx context.Context, params ExchangeParams) (ExchangeResult, error) {
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:token-exchange")
	form.Set("client_assertion_type", params.ClientAssertionType)
	form.Set("client_assertion", params.ClientAssertion)
	form.Set("subject_token_type", params.SubjectTokenType)
	form.Set("subject_token", params.SubjectToken)
	if params.ActorToken != "" {
		form.Set("actor_token", params.ActorToken)
		form.Set("actor_token_type", params.ActorTokenType)
	}
	form.Set("audience", params.Audience)
	if len(params.Scopes) > 0 {
		form.Set("scope", strings.Join(params.Scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("failed to create exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("failed to send exchange request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("failed to read exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return ExchangeResult{}, fmt.Errorf("exchange request failed with status %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	err = json.Unmarshal(body, &tokenResp)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("failed to unmarshal exchange response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return ExchangeResult{}, errors.New("token not found in exchange response")
	}

	return ExchangeResult{Token: tokenResp.AccessToken}, nil
}
