package mono

import (
	"encoding/json"
	"testing"
)

func TestInitiateResponseUnmarshalWrappedData(t *testing.T) {
	var resp InitiateResponse
	err := json.Unmarshal([]byte(`{"data":{"client_token":"mono_token","expires_in":3600}}`), &resp)
	if err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Data.ClientToken != "mono_token" {
		t.Fatalf("client token = %q, want mono_token", resp.Data.ClientToken)
	}
	if resp.Data.ExpiresIn != 3600 {
		t.Fatalf("expires in = %d, want 3600", resp.Data.ExpiresIn)
	}
}

func TestInitiateResponseUnmarshalTopLevelData(t *testing.T) {
	var resp InitiateResponse
	err := json.Unmarshal([]byte(`{"client_token":"mono_token","expires_in":3600}`), &resp)
	if err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Data.ClientToken != "mono_token" {
		t.Fatalf("client token = %q, want mono_token", resp.Data.ClientToken)
	}
	if resp.Data.ExpiresIn != 3600 {
		t.Fatalf("expires in = %d, want 3600", resp.Data.ExpiresIn)
	}
}

func TestInitiateResponseUnmarshalMonoURLData(t *testing.T) {
	var resp InitiateResponse
	err := json.Unmarshal([]byte(`{"status":"successful","data":{"mono_url":"https://link.mono.co/ALB65XCPXCOU","scope":"auth","institution":{},"redirect_url":"trackiq://mono/callback","is_multi":false,"created_at":"2026-07-03T14:16:56.839Z"}}`), &resp)
	if err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Data.MonoURL != "https://link.mono.co/ALB65XCPXCOU" {
		t.Fatalf("mono url = %q, want https://link.mono.co/ALB65XCPXCOU", resp.Data.MonoURL)
	}
	if resp.Data.Scope != "auth" {
		t.Fatalf("scope = %q, want auth", resp.Data.Scope)
	}
	if resp.Data.RedirectURL != "trackiq://mono/callback" {
		t.Fatalf("redirect url = %q, want trackiq://mono/callback", resp.Data.RedirectURL)
	}
}

func TestExchangeResponseUnmarshalAccountIDData(t *testing.T) {
	var resp ExchangeResponse
	err := json.Unmarshal([]byte(`{"status":"successful","message":"Request was succesfully completed","timestamp":"2026-07-03T20:54:24.528Z","data":{"id":"6a48216f994f1194174b9105"}}`), &resp)
	if err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Data.Account.ID != "6a48216f994f1194174b9105" {
		t.Fatalf("account id = %q, want 6a48216f994f1194174b9105", resp.Data.Account.ID)
	}
}

func TestExchangeResponseUnmarshalNestedAccountData(t *testing.T) {
	var resp ExchangeResponse
	err := json.Unmarshal([]byte(`{"data":{"account":{"id":"mono_acc_123","name":"Primary","account_number":"0123456789","bank_code":"001","currency":"NGN","provider":"mono"}}}`), &resp)
	if err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Data.Account.ID != "mono_acc_123" {
		t.Fatalf("account id = %q, want mono_acc_123", resp.Data.Account.ID)
	}
	if resp.Data.Account.Name != "Primary" {
		t.Fatalf("account name = %q, want Primary", resp.Data.Account.Name)
	}
}
