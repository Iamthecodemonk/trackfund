package mono

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

var (
	ErrRequestTimeout             = errors.New("mono request timed out")
	ErrInvalidLinkSessionResponse = errors.New("mono link session response missing mono_url or client_token")
)

type RequestError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("mono request failed: status=%d body=%s", e.StatusCode, e.Body)
}

type ResponseParseError struct {
	Method string
	URL    string
	Body   string
	Err    error
}

func (e *ResponseParseError) Error() string {
	return fmt.Sprintf("mono response parse failed: %v", e.Err)
}

func (e *ResponseParseError) Unwrap() error {
	return e.Err
}

type Client struct {
	BaseURL    string
	SecretKey  string
	PublicKey  string
	Debug      bool
	HTTPClient *http.Client
}

// NewClient creates a Mono client with the provided baseURL and keys.
func NewClient(baseURL, secret, pub string) *Client {
	return NewClientWithTimeout(baseURL, secret, pub, 30*time.Second)
}

func NewClientWithTimeout(baseURL, secret, pub string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		BaseURL:    baseURL,
		SecretKey:  secret,
		PublicKey:  pub,
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRequestTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
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
	if c.Debug {
		slog.Info("mono.request",
			slog.String("method", method),
			slog.String("url", req.URL.String()),
			slog.Any("headers", sanitizedRequestHeaders(req.Header)),
			slog.Bool("secret_key_configured", c.SecretKey != ""),
			slog.String("body", buf.String()),
		)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if IsTimeoutError(err) {
			return ErrRequestTimeout
		}
		return err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return readErr
	}
	if c.Debug {
		slog.Info("mono.response",
			slog.String("method", method),
			slog.String("url", req.URL.String()),
			slog.Int("status", resp.StatusCode),
			slog.String("body", string(respBody)),
		)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &RequestError{
			Method:     method,
			URL:        req.URL.String(),
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return &ResponseParseError{
				Method: method,
				URL:    req.URL.String(),
				Body:   string(respBody),
				Err:    err,
			}
		}
	}
	return nil
}

func sanitizedRequestHeaders(headers http.Header) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if key == "Mono-Sec-Key" {
			out[key] = []string{"<redacted>"}
			continue
		}
		out[key] = values
	}
	return out
}

// Concrete response shapes used by Mono APIs (partial)
type InitiateResponse struct {
	Data LinkSessionData `json:"data"`
}

type LinkSessionData struct {
	ClientToken string         `json:"client_token,omitempty"`
	ExpiresIn   int            `json:"expires_in,omitempty"`
	MonoURL     string         `json:"mono_url,omitempty"`
	Scope       string         `json:"scope,omitempty"`
	Institution map[string]any `json:"institution,omitempty"`
	RedirectURL string         `json:"redirect_url,omitempty"`
	IsMulti     bool           `json:"is_multi,omitempty"`
	CreatedAt   string         `json:"created_at,omitempty"`
}

func (r *InitiateResponse) UnmarshalJSON(data []byte) error {
	var wrapped struct {
		Data LinkSessionData `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	if wrapped.Data.ClientToken != "" || wrapped.Data.MonoURL != "" || wrapped.Data.ExpiresIn > 0 {
		r.Data = wrapped.Data
		return nil
	}

	var topLevel LinkSessionData
	if err := json.Unmarshal(data, &topLevel); err != nil {
		return err
	}
	r.Data = topLevel
	return nil
}

type ExchangeResponse struct {
	Data struct {
		Account MonoAccount `json:"account"`
	} `json:"data"`
}

func (r *ExchangeResponse) UnmarshalJSON(data []byte) error {
	var wrapped struct {
		Data struct {
			Account MonoAccount `json:"account"`
			ID      string      `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	r.Data.Account = wrapped.Data.Account
	if r.Data.Account.ID == "" {
		r.Data.Account.ID = wrapped.Data.ID
	}
	return nil
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
	if resp.Data.ClientToken == "" && resp.Data.MonoURL == "" {
		return InitiateResponse{}, ErrInvalidLinkSessionResponse
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
