package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

func ReadAndDeleteAccessURLFile(filepath string) (string, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return "", fmt.Errorf("error reading AccessUrl path: %v", err)
	}

	parsedUrl, err := url.Parse(string(data))
	if err != nil {
		return "", fmt.Errorf("error parsing AccessUrl: %v", err)
	}

	err = os.Remove(filepath)
	if err != nil {
		return "", fmt.Errorf("error removing AccessUrl file: %v", err)
	}

	return parsedUrl.String(), nil
}

// AccountMapping represents a mapping from account ID to custom name
type AccountMapping struct {
	AccountID   string `json:"account_id"`
	CustomName  string `json:"custom_name"`
}

// AccountMappingConfig holds all account mappings and ignore lists
type AccountMappingConfig struct {
	Mappings      []AccountMapping `json:"mappings"`
	IgnoreList    []string        `json:"ignore_list"`
}

// LoadAccountMappings loads account mappings from a file or returns empty config if file doesn't exist
func LoadAccountMappings(filepath string) (*AccountMappingConfig, error) {
	if filepath == "" {
		return &AccountMappingConfig{Mappings: []AccountMapping{}}, nil
	}

	data, err := os.ReadFile(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			return &AccountMappingConfig{Mappings: []AccountMapping{}}, nil
		}
		return nil, fmt.Errorf("error reading account mappings file: %v", err)
	}

	var config AccountMappingConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing account mappings JSON: %v", err)
	}

	return &config, nil
}

// GetAccountNameMapping returns the custom name for an account ID, or the original name if no mapping exists
func (config *AccountMappingConfig) GetAccountNameMapping(accountID, originalName string) string {
	for _, mapping := range config.Mappings {
		if mapping.AccountID == accountID {
			return mapping.CustomName
		}
	}
	return originalName
}

// IsAccountIgnored checks if an account ID should be ignored/excluded from metrics
func (config *AccountMappingConfig) IsAccountIgnored(accountID string) bool {
	for _, ignoredID := range config.IgnoreList {
		if ignoredID == accountID {
			return true
		}
	}
	return false
}
