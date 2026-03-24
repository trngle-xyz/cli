package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/trngle-xyz/cli/internal/api"
	"github.com/trngle-xyz/cli/internal/config"
	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/history"
	"github.com/trngle-xyz/cli/internal/tui"
	"github.com/trngle-xyz/cli/internal/wallet"
	"github.com/trngle-xyz/cli/internal/wallet/loop"
)

var (
	configFile   string
	apiOnly      bool
	apiPort      int
	devSetup     bool
	trngleAPIURL string
)

// apiServer holds the running API server so we can shut it down cleanly.
var apiServer *api.Server

var rootCmd = &cobra.Command{
	Use:     "trngle",
	Short:   "TRNGLE — swap tokens on Canton Network",
	Version: tui.Version,
	Long: `A terminal application for trading on Canton Network.

Get started:
  trngle              Launch the interactive terminal
  trngle --version    Show version`,
	RunE: runApp,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "config file path")
	rootCmd.Flags().BoolVarP(&apiOnly, "api-only", "a", false, "run API server only (no TUI)")
	rootCmd.Flags().IntVarP(&apiPort, "port", "p", 8080, "API server port")
	rootCmd.Flags().BoolVar(&devSetup, "dev-setup", false, "force setup wizard using a temporary config for rapid onboarding UI testing")
	rootCmd.Flags().StringVar(&trngleAPIURL, "trngle-api-url", "", "override operator/TRNGLE API URL for quote flow")
}

func runApp(cmd *cobra.Command, args []string) error {
	if apiOnly {
		return runAPIOnly()
	}
	return runTUI()
}

func runAPIOnly() error {
	cfg, _, _, err := loadConfig()
	if err != nil {
		return err
	}
	applyRuntimeOverrides(&cfg)

	addr := fmt.Sprintf("%s:%d", cfg.LocalAPI.Bind, apiPort)

	walletAdapter := newWalletAdapter(cfg)
	if cfg.PrivateKeyHex != "" {
		if err := walletAdapter.Initialize(cfg.PrivateKeyHex); err != nil {
			return fmt.Errorf("initialize wallet adapter: %w", err)
		}
	}

	historyDB, err := openHistoryDB()
	if err != nil {
		return err
	}
	defer historyDB.Close()

	quoteClient := core.NewQuoteClient(cfg.TrngleAPIURL, cfg.TrngleAPIKey)
	var serverOpts []api.ServerOption
	if (cfg.LocalAPI.AuthMode == "auto-token" || cfg.LocalAPI.AuthMode == "fixed-token") && cfg.LocalAPI.FixedToken != "" {
		serverOpts = append(serverOpts, api.WithAuth(cfg.LocalAPI.FixedToken))
	}
	if cfg.TrngleAPIURL != "" && cfg.WalletPartyID != "" {
		serverOpts = append(serverOpts, api.WithNotifyURL(cfg.TrngleAPIURL, cfg.WalletPartyID))
	}
	server := api.NewServer(addr, walletAdapter, quoteClient, historyDB, serverOpts...)
	fmt.Printf("Starting local API server at http://%s\n", addr)

	// Graceful shutdown on signal.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	return server.Start()
}

func runTUI() error {
	cfg, created, path, err := loadConfig()
	if err != nil {
		return err
	}
	applyRuntimeOverrides(&cfg)

	forceSetup := created || devSetup || cfg.PrivateKeyHex == ""

	// Open local history DB for the TUI (shared with API server via same path).
	historyDB, err := openHistoryDB()
	if err != nil {
		historyDB = nil
	}

	// Allow the TUI model to start the API server after wizard completes.
	tui.StartAPIServerFunc = func(newCfg config.AppConfig) {
		shutdownAPIServer() // stop old one if running
		if newCfg.LocalAPI.Enabled {
			startAPIServerBackground(newCfg)
		}
	}
	tui.ShutdownAPIServerFunc = shutdownAPIServer

	model := tui.NewModelFromConfig(cfg, path, forceSetup)
	if historyDB != nil {
		model.SetHistoryDB(historyDB)
	}

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		if historyDB != nil {
			historyDB.Close()
		}
		return fmt.Errorf("error running TUI: %w", err)
	}

	// Clean shutdown.
	if historyDB != nil {
		historyDB.Close()
	}
	shutdownAPIServer()

	return nil
}

func applyRuntimeOverrides(cfg *config.AppConfig) {
	if cfg == nil {
		return
	}
	if v := strings.TrimSpace(trngleAPIURL); v != "" {
		cfg.TrngleAPIURL = v
	}
}

// startAPIServerBackground launches the local API server in a goroutine.
func startAPIServerBackground(cfg config.AppConfig) {
	addr := fmt.Sprintf("%s:%d", cfg.LocalAPI.Bind, cfg.LocalAPI.Port)

	walletAdapter := newWalletAdapter(cfg)
	if cfg.PrivateKeyHex != "" {
		_ = walletAdapter.Initialize(cfg.PrivateKeyHex)
	}

	historyDB, err := openHistoryDB()
	if err != nil {
		return
	}

	var serverOpts []api.ServerOption
	if (cfg.LocalAPI.AuthMode == "auto-token" || cfg.LocalAPI.AuthMode == "fixed-token") && cfg.LocalAPI.FixedToken != "" {
		serverOpts = append(serverOpts, api.WithAuth(cfg.LocalAPI.FixedToken))
	}
	if cfg.TrngleAPIURL != "" && cfg.WalletPartyID != "" {
		serverOpts = append(serverOpts, api.WithNotifyURL(cfg.TrngleAPIURL, cfg.WalletPartyID))
	}
	apiServer = api.NewServer(addr, walletAdapter, core.NewQuoteClient(cfg.TrngleAPIURL, cfg.TrngleAPIKey), historyDB, serverOpts...)

	go func() {
		if err := apiServer.Start(); err != nil && err.Error() != "http: Server closed" {
			_ = err // suppress — TUI alt screen cannot display log output
		}
		historyDB.Close()
	}()

	// Give the server a moment to bind the port before the TUI checks health.
	time.Sleep(100 * time.Millisecond)
}

// shutdownAPIServer gracefully stops the running API server.
func shutdownAPIServer() error {
	if apiServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := apiServer.Shutdown(ctx); err != nil {
			return err
		}
		apiServer = nil
	}
	return nil
}

func openHistoryDB() (*history.Store, error) {
	historyPath, err := history.DefaultPath()
	if err != nil {
		return nil, err
	}
	return history.Open(historyPath)
}

func newWalletAdapter(_ config.AppConfig) wallet.WalletAdapter {
	return loop.NewAdapter()
}

func loadConfig() (config.AppConfig, bool, string, error) {
	path := configFile
	if devSetup {
		tmpDir, err := os.MkdirTemp("", "trngle-setup-dev-*")
		if err != nil {
			return config.AppConfig{}, false, "", fmt.Errorf("create temp dir for dev setup: %w", err)
		}
		path = filepath.Join(tmpDir, "config.json")
		if err := config.Save(path, config.Default()); err != nil {
			return config.AppConfig{}, false, "", fmt.Errorf("seed temp config: %w", err)
		}
		verifyCfg, err := config.Load(path)
		if err != nil || verifyCfg.WalletProvider == "" {
			return config.AppConfig{}, false, "", fmt.Errorf("verify temp config: %w", err)
		}
		fmt.Printf("DEV setup mode using temp config: %s\n", path)
	}
	if path == "" {
		defaultPath, err := config.DefaultConfigPath()
		if err != nil {
			return config.AppConfig{}, false, "", err
		}
		path = defaultPath
	}
	cfg, created, err := config.LoadOrCreate(path)
	if err != nil {
		return config.AppConfig{}, false, "", fmt.Errorf("load config: %w", err)
	}
	return cfg, created, path, nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
