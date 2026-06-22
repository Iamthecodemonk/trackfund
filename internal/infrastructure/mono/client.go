package mono

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	BaseURL    string
	SecretKey  string
	PublicKey  string
	HTTPClient *http.Client
}

// NewClient creates a Mono client with the provided baseURL and keys.
// Pass an empty baseURL to use the default https://api.withmono.com/v2
func NewClient(baseURL, secret, pub string) *Client {
	if baseURL == "" {
		baseURL = "https://api.withmono.com/v2"
	}
	return &Client{
		BaseURL:    baseURL,
		SecretKey:  secret,
		PublicKey:  pub,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.SecretKey != "" {
		// Mono expects the secret in the mono-sec-key header
		req.Header.Set("mono-sec-key", c.SecretKey)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// try to read body for error details
		b, _ := io.ReadAll(resp.Body)
		return errors.New(fmt.Sprintf("mono request failed: %s", string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Concrete response shapes used by Mono APIs (partial)
type InitiateResponse struct {
	Data struct {
		ClientToken string `json:"client_token"`
		ExpiresIn   int    `json:"expires_in"`
	} `json:"data"`
}

type ExchangeResponse struct {
	Data struct {
		Account MonoAccount `json:"account"`
	} `json:"data"`
}

type MonoAccount struct {
	ID            string `json:"id"` // mono account id
	Name          string `json:"name"`
	AccountNumber string `json:"account_number"`
	BankCode      string `json:"bank_code"`
	Currency      string `json:"currency"`
	Provider      string `json:"provider"`
}

type TransactionsResponse struct {
	Data struct {
		Transactions []MonoTransaction `json:"transactions"`
	} `json:"data"`
}

type MonoTransaction struct {
	ID          string `json:"id"`
	AccountID   string `json:"account_id"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	PaidAt      string `json:"paid_at"`
	Description string `json:"description"`
}

func (c *Client) CreateLinkSession(ctx context.Context, payload map[string]any) (InitiateResponse, error) {
	var resp InitiateResponse
	if err := c.doRequest(ctx, "POST", "/accounts/initiate", payload, &resp); err != nil {
		return InitiateResponse{}, err
	}
	return resp, nil
}

func (c *Client) ExchangeCode(ctx context.Context, code string) (ExchangeResponse, error) {
	var resp ExchangeResponse
	payload := map[string]any{"code": code}
	if err := c.doRequest(ctx, "POST", "/accounts/auth", payload, &resp); err != nil {
		return ExchangeResponse{}, err
	}
	return resp, nil
}

func (c *Client) GetAccounts(ctx context.Context) (TransactionsResponse, error) {
	// reuse TransactionsResponse for a generic data wrapper; real API has accounts listing
	var resp TransactionsResponse
	if err := c.doRequest(ctx, "GET", "/accounts", nil, &resp); err != nil {
		return TransactionsResponse{}, err
	}
	return resp, nil
}

func (c *Client) GetTransactions(ctx context.Context, monoAccountID string) (TransactionsResponse, error) {
	var resp TransactionsResponse
	path := fmt.Sprintf("/accounts/%s/transactions", monoAccountID)
	if err := c.doRequest(ctx, "GET", path, nil, &resp); err != nil {
		return TransactionsResponse{}, err
	}
	return resp, nil
}
