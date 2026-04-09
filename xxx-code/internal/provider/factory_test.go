package provider

import (
	"reflect"
	"testing"

	"github.com/caowenhua/x-agent/xxx-code/internal/config"
	anthropicprovider "github.com/caowenhua/x-agent/xxx-code/internal/provider/anthropic"
	openaiprovider "github.com/caowenhua/x-agent/xxx-code/internal/provider/openai"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: ProviderAnthropic},
		{input: " anthropic ", want: ProviderAnthropic},
		{input: "openai", want: ProviderOpenAI},
		{input: "azure", want: ProviderAzureOpenAI},
		{input: "azure_openai", want: ProviderAzureOpenAI},
		{input: "azure-openai", want: ProviderAzureOpenAI},
		{input: "custom-provider", want: "custom-provider"},
	}

	for _, test := range tests {
		if got := Normalize(test.input); got != test.want {
			t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestNewSelectsProviderImplementation(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		assert   func(t *testing.T, value any)
	}{
		{
			name:     "default anthropic",
			provider: "",
			assert: func(t *testing.T, value any) {
				t.Helper()
				if _, ok := value.(*anthropicprovider.Client); !ok {
					t.Fatalf("expected anthropic client, got %T", value)
				}
			},
		},
		{
			name:     "explicit anthropic",
			provider: "anthropic",
			assert: func(t *testing.T, value any) {
				t.Helper()
				if _, ok := value.(*anthropicprovider.Client); !ok {
					t.Fatalf("expected anthropic client, got %T", value)
				}
			},
		},
		{
			name:     "openai",
			provider: "openai",
			assert: func(t *testing.T, value any) {
				t.Helper()
				client, ok := value.(*openaiprovider.Client)
				if !ok {
					t.Fatalf("expected openai client, got %T", value)
				}
				authMode := reflect.ValueOf(client).Elem().FieldByName("authMode").String()
				if authMode != string(openaiprovider.AuthModeBearer) {
					t.Fatalf("expected bearer auth mode, got %s", authMode)
				}
			},
		},
		{
			name:     "azure openai",
			provider: "azure",
			assert: func(t *testing.T, value any) {
				t.Helper()
				client, ok := value.(*openaiprovider.Client)
				if !ok {
					t.Fatalf("expected azure openai client, got %T", value)
				}
				authMode := reflect.ValueOf(client).Elem().FieldByName("authMode").String()
				if authMode != string(openaiprovider.AuthModeAPIKey) {
					t.Fatalf("expected api_key auth mode, got %s", authMode)
				}
			},
		},
		{
			name:     "unknown falls back to anthropic",
			provider: "something-else",
			assert: func(t *testing.T, value any) {
				t.Helper()
				if _, ok := value.(*anthropicprovider.Client); !ok {
					t.Fatalf("expected anthropic fallback client, got %T", value)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := New(config.Config{
				Provider: test.provider,
				APIKey:   "test-key",
				BaseURL:  "https://example.invalid",
				Version:  "2023-06-01",
			})
			test.assert(t, client)
		})
	}
}
