package auth

import (
	"net/http"
	"testing"
)

func TestGetAPIKey(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "ApiKey 1234567890")
	apiKey, err := GetAPIKey(headers)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if apiKey != "1234567890" {
		t.Errorf("expected api key to be 1234567890, got %v", apiKey)
	}
}
