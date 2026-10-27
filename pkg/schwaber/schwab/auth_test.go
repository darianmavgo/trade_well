package schwab

import (
	"strings"
	"testing"
)

func TestAuthorizeURL(t *testing.T) {
	c := &Client{
		appKey:      "test_client_id_123",
		callbackURL: "https://127.0.0.1",
	}

	authURL := c.AuthorizeURL()
	if !strings.HasPrefix(authURL, AuthURL) {
		t.Errorf("expected URL to start with %s, got %s", AuthURL, authURL)
	}
	if !strings.Contains(authURL, "client_id=test_client_id_123") {
		t.Errorf("expected client_id in authURL, got %s", authURL)
	}
	if !strings.Contains(authURL, "redirect_uri=https%3A%2F%2F127.0.0.1") {
		t.Errorf("expected encoded redirect_uri in authURL, got %s", authURL)
	}
}

func TestExtractCodeFromURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "Standard callback URL with encoded @",
			input:    "https://127.0.0.1/?code=C0.b2F1dGgyLmNkYy5zY2h3YWIuY29t.I7yY83L8%40&session=xyz",
			expected: "C0.b2F1dGgyLmNkYy5zY2h3YWIuY29t.I7yY83L8@",
			wantErr:  false,
		},
		{
			name:     "Callback URL with port",
			input:    "https://127.0.0.1:8182/?code=MOCK_CODE_123%40",
			expected: "MOCK_CODE_123@",
			wantErr:  false,
		},
		{
			name:     "Raw code with decoded @",
			input:    "C0.b2F1dGgyLmNkYy5zY2h3YWIuY29t.I7yY83L8@",
			expected: "C0.b2F1dGgyLmNkYy5zY2h3YWIuY29t.I7yY83L8@",
			wantErr:  false,
		},
		{
			name:     "Raw code with encoded %40",
			input:    "MOCK_CODE_123%40",
			expected: "MOCK_CODE_123@",
			wantErr:  false,
		},
		{
			name:    "Empty input",
			input:   "   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractCodeFromURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExtractCodeFromURL() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("ExtractCodeFromURL() got = %s, want %s", got, tt.expected)
			}
		})
	}
}
