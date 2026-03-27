package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/exporter"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/monitors"
)

var (
	buildTimeUTC string
	commit       string
	version      string
)

func printBuildInfo(w io.Writer) {
	fmt.Fprint(w, "\nBuild Info:\n")
	fmt.Fprintf(w, "  Version:    %s\n", version)
	fmt.Fprintf(w, "  Commit:     %s\n", commit)
	fmt.Fprintf(w, "  Build time: %s\n", buildTimeUTC)
}

func printTopLevelUsage() {
	w := flag.CommandLine.Output()
	fmt.Fprint(w, "Usage: hl_exporter <command> [flags]\n")
	fmt.Fprint(w, "\nCommands:\n")
	fmt.Fprint(w, "  start       Start the Hyperliquid metrics exporter\n")
	fmt.Fprint(w, "\nGlobal flags:\n")
	fmt.Fprint(w, "  --help      Show this help message\n")
	fmt.Fprint(w, "  --version   Print version and exit\n")
	fmt.Fprint(w, "\nRun 'hl_exporter <command> --help' for flag details.\n")
	printBuildInfo(w)
}

func main() {
	if len(os.Args) < 2 {
		printTopLevelUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "--help", "-h", "help":
		printTopLevelUsage()
		os.Exit(0)
	case "--version", "-version", "version":
		fmt.Printf("hl_exporter version %s (commit %s, build time %s)\n",
			version, commit, buildTimeUTC)
		os.Exit(0)
	case "start":
		// handled below
	default:
		fmt.Fprintf(os.Stderr, "%q is not a valid command.\n\n", os.Args[1])
		printTopLevelUsage()
		os.Exit(1)
	}

	startCmd := flag.NewFlagSet("start", flag.ExitOnError)
	startCmd.Usage = func() {
		w := startCmd.Output()
		fmt.Fprint(w, "Start the Hyperliquid metrics exporter.\n")
		fmt.Fprint(w, "\nUsage: hl_exporter start [flags]\n")
		fmt.Fprint(w, "\nFlags:\n")
		startCmd.PrintDefaults()
		printBuildInfo(w)
	}

	logLevel := startCmd.String("log-level", "info", "Log level (debug, info, warning, error)")
	enableOTLP := startCmd.Bool("otlp", false, "Enable OTLP export")
	otlpEndpoint := startCmd.String("otlp-endpoint", "", "OTLP endpoint (required when OTLP is enabled)")
	nodeHome := startCmd.String("node-home", "", "Node home directory (overrides env var)")
	nodeBinary := startCmd.String("node-binary", "", "Node binary path (overrides env var)")
	alias := startCmd.String("alias", "", "Node alias (required when OTLP is enabled)")
	chain := startCmd.String("chain", "", "Chain type ('mainnet' or 'testnet')")
	otlpInsecure := startCmd.Bool("otlp-insecure", false, "Use insecure connection for OTLP")
	enableEVM := startCmd.Bool("evm-metrics", false, "Enable EVM monitoring")
	contractMetrics := startCmd.Bool("contract-metrics", false, "Enable per-contract transaction metrics")
	contractLimit := startCmd.Int("contract-metrics-limit", 20, "Maximum number of individual contract labels to retain")
	enableReplicaMetrics := startCmd.Bool("replica-metrics", false, "Enable replica commands transaction metrics")
	enableValidatorRTT := startCmd.Bool("validator-rtt", false, "Enable validator RTT monitoring")

	if err := startCmd.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if err := logger.SetLogLevel(*logLevel); err != nil {
		fmt.Printf("Error setting log level: %v\n", err)
		os.Exit(1)
	}

	if *chain != "" {
		*chain = strings.ToLower(*chain)
		if *chain != "mainnet" && *chain != "testnet" {
			logger.Error("--chain flag must be either 'mainnet' or 'testnet' (case insensitive)")
			os.Exit(1)
		}
		*chain = strings.ToLower(*chain)
	}

	flags := &config.Flags{
		NodeHome:              *nodeHome,
		NodeBinary:            *nodeBinary,
		Chain:                 *chain,
		EnableEVM:             *enableEVM,
		EnableContractMetrics: *contractMetrics,
		ContractMetricsLimit:  *contractLimit,
		EnableCoreTxMetrics:   false,
		UseLiveState:          false,
		EnableReplicaMetrics:  *enableReplicaMetrics,
		ReplicaDataDir:        "",                 // Always use default
		ReplicaBufferSize:     8,                  // Always use default 8MB
		EVMBlockTypeMetrics:   *enableEVM,         // Always enable block type metrics when EVM is enabled
		EnableValidatorRTT:    enableValidatorRTT, // Use the bool pointer directly
	}

	cfg := config.LoadConfig(flags)

	if *enableOTLP {
		if *alias == "" {
			logger.Error("--alias flag is required when OTLP is enabled. This can be whatever you choose and is just an identifier for your node.")
			os.Exit(1)
		}
		if *chain == "" {
			logger.Error("--chain flag is required when OTLP is enabled")
			os.Exit(1)
		}
		if *otlpEndpoint == "" {
			logger.Error("--otlp-endpoint flag is required when OTLP is enabled")
			os.Exit(1)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// After loading config, before metrics initialization
	validatorAddress, isValidator := monitors.GetValidatorStatus(cfg.NodeHome)

	// Pre-populate signer mappings before any monitors start
	if err := monitors.PopulateSignerMappings(cfg.NodeHome); err != nil {
		logger.Warning("Failed to pre-populate signer mappings: %v", err)
		// Non-fatal - continue startup
	}

	// Initialize metrics configuration
	metricsConfig := metrics.MetricsConfig{
		EnablePrometheus: true, // Always enable Prometheus - it's the core functionality
		EnableOTLP:       *enableOTLP,
		OTLPEndpoint:     *otlpEndpoint,
		OTLPInsecure:     *otlpInsecure,
		Alias:            *alias,
		Chain:            *chain,
		NodeHome:         cfg.NodeHome,
		ValidatorAddress: validatorAddress,
		IsValidator:      isValidator,
		EnableEVM:        *enableEVM,
	}

	if err := metrics.InitMetrics(ctx, metricsConfig); err != nil {
		logger.Error("Failed to initialize metrics: %v", err)
		os.Exit(1)
	}

	exporter.Start(ctx, cfg)

	<-ctx.Done()
	logger.InfoComponent("system", "Shutting down gracefully")
}
