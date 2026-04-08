package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type fileTokenPayload struct {
	Token  string   `json:"token,omitempty"`
	Tokens []string `json:"tokens,omitempty"`
}

func CurrentToken(staticToken, tokenFile string) (string, error) {
	tokens, err := CurrentTokens(staticToken, tokenFile)
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "", nil
	}
	return tokens[0], nil
}

func CurrentTokens(staticToken, tokenFile string) ([]string, error) {
	merged := normalizeTokens([]string{strings.TrimSpace(staticToken)})
	if strings.TrimSpace(tokenFile) == "" {
		return merged, nil
	}
	fileTokens, err := ReadTokenFile(tokenFile)
	if err != nil {
		return nil, err
	}
	return appendUniqueTokens(fileTokens, merged...), nil
}

func ReadTokenFile(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tokens, err := ParseTokens(data)
	if err != nil {
		return nil, fmt.Errorf("parse token file %s: %w", path, err)
	}
	return tokens, nil
}

func ParseTokens(data []byte) ([]string, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}

	switch trimmed[0] {
	case '[':
		var values []string
		if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
			return nil, err
		}
		return normalizeTokens(values), nil
	case '{':
		var payload fileTokenPayload
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return nil, err
		}
		return appendUniqueTokens(normalizeTokens(payload.Tokens), strings.TrimSpace(payload.Token)), nil
	case '"':
		var value string
		if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
			return nil, err
		}
		return normalizeTokens([]string{value}), nil
	default:
		fields := strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		})
		return normalizeTokens(fields), nil
	}
}

func appendUniqueTokens(base []string, values ...string) []string {
	merged := append([]string(nil), base...)
	seen := make(map[string]struct{}, len(merged))
	for _, value := range merged {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func normalizeTokens(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
