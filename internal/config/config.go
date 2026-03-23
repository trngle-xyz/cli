package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultDirName  = ".trngle"
	defaultFileName = "config.json"
)

type LocalAPIConfig struct {
	Enabled    bool   `json:"enabled"`
	Port       int    `json:"port"`
	Bind       string `json:"bind"`
	AuthMode   string `json:"auth_mode"`
	FixedToken string `json:"fixed_token"`
}

type AppConfig struct {
	WalletProvider string         `json:"wallet_provider"`
	WalletPartyID  string         `json:"wallet_party_id"`
	QuotePartyID   string         `json:"quote_party_id"`
	PrivateKeyHex  string         `json:"private_key_hex"`
	Network        string         `json:"network"`
	TrngleAPIURL   string         `json:"trngle_api_url"`
	TrngleAPIKey   string         `json:"trngle_api_key"`
	LocalAPI       LocalAPIConfig `json:"local_api"`
	Theme          string         `json:"theme"`
	Language       string         `json:"language"`
}

// NetworkAPIURLs maps network names to their default operator API endpoints.
var NetworkAPIURLs = map[string]string{
	"mainnet": "https://api.trngle.com",
	"testnet": "https://api.trngle.xyz",
	"devnet":  "https://api.trngle.xyz",
}

// DefaultAPIURL is the fallback when no network-specific URL is configured.
const DefaultAPIURL = "https://api.trngle.com"

func Default() AppConfig {
	return AppConfig{
		WalletProvider: "loop",
		QuotePartyID:   "",
		PrivateKeyHex:  "",
		Network:        "mainnet",
		TrngleAPIURL:   DefaultAPIURL,
		LocalAPI: LocalAPIConfig{
			Enabled:    false,
			Port:       8080,
			Bind:       "127.0.0.1",
			AuthMode:   "none",
			FixedToken: "",
		},
		Theme:    "cyan",
		Language: "en",
	}
}

// APIURLForNetwork returns the appropriate operator API URL for a given network.
func APIURLForNetwork(network string) string {
	if url, ok := NetworkAPIURLs[network]; ok {
		return url
	}
	return DefaultAPIURL
}

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, defaultDirName, defaultFileName), nil
}

func LoadOrCreate(path string) (AppConfig, bool, error) {
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return AppConfig{}, false, err
		}
	}

	cfg, err := Load(path)
	if err == nil {
		return cfg, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return AppConfig{}, false, err
	}

	cfg = Default()
	if err := Save(path, cfg); err != nil {
		return AppConfig{}, false, err
	}
	return cfg, true, nil
}

func Load(path string) (AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}, err
	}

	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AppConfig{}, fmt.Errorf("parse config file %q: %w", path, err)
	}

	cfg.applyDefaults()
	return cfg, nil
}

func Save(path string, cfg AppConfig) error {
	cfg.applyDefaults()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func (c *AppConfig) applyDefaults() {
	def := Default()

	if c.WalletProvider == "" {
		c.WalletProvider = def.WalletProvider
	}
	// QuotePartyID intentionally left empty if not set — the wallet party ID
	// is used as fallback at runtime (see commands.go fetchBalancesCmd).
	if c.Network == "" {
		c.Network = def.Network
	}
	if c.TrngleAPIURL == "" {
		c.TrngleAPIURL = def.TrngleAPIURL
	}
	if c.LocalAPI.Port == 0 {
		c.LocalAPI.Port = def.LocalAPI.Port
	}
	if c.LocalAPI.Bind == "" {
		c.LocalAPI.Bind = def.LocalAPI.Bind
	}
	if c.LocalAPI.AuthMode == "" {
		c.LocalAPI.AuthMode = def.LocalAPI.AuthMode
	}
	if c.Theme == "" {
		c.Theme = def.Theme
	}
	if c.Language == "" {
		c.Language = def.Language
	}
}
