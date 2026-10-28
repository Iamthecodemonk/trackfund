package profile

import (
	"encoding/json"
	"testing"
)

func TestProfileValidationAndNormalization(t *testing.T) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{"fullName":" Alice Example ","email":"NEW@EXAMPLE.COM","phoneNumber":"+234 (801) 234-5678","photoUrl":"https://cdn.example.com/avatar.png?signature=test"}`), &body); err != nil {
		t.Fatal(err)
	}
	c, err := ParseChanges(body)
	if err != nil {
		t.Fatal(err)
	}
	if *c.FullName.Value != "Alice Example" || *c.Email.Value != "new@example.com" || *c.PhoneNumber.Value != "+2348012345678" {
		t.Fatal("fields were not normalized")
	}
	body = nil
	json.Unmarshal([]byte(`{"phoneNumber":null,"photoUrl":null}`), &body)
	c, err = ParseChanges(body)
	if err != nil || !c.PhotoURL.Set || c.PhotoURL.Value != nil || !c.PhoneNumber.Set {
		t.Fatal("nullable fields cannot be cleared", err)
	}
	for _, bad := range []string{`{}`, `{"fullName":" "}`, `{"email":null}`, `{"email":"Name <person@example.com>"}`, `{"email":"bad"}`, `{"phoneNumber":"08012"}`, `{"photoUrl":"javascript:alert(1)"}`, `{"role":"ADMIN"}`, `{"id":"another-user"}`, `{"fullName":"Alice\nInjected"}`} {
		body = nil
		json.Unmarshal([]byte(bad), &body)
		if _, err := ParseChanges(body); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestLocalPhoneNumberAccepted(t *testing.T) {
	var body map[string]json.RawMessage
	_ = json.Unmarshal([]byte("{\"phoneNumber\":\"08012345678\"}"), &body)
	c, err := ParseChanges(body)
	if err != nil || *c.PhoneNumber.Value != "08012345678" {
		t.Fatal("local phone rejected", err)
	}
}
