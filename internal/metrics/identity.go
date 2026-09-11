package metrics

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
)

// getPublicIP resolves this node's public IP for the server_ip resource
// attribute. hl-node writes its detected public IP to
// $NODE_HOME/last_known_public_ip.json, so prefer that (no egress needed)
// and fall back to api.ipify.org with a short timeout. Returns "" when
// neither works.
func getPublicIP(nodeHome string) string {
	if nodeHome != "" {
		raw, err := os.ReadFile(filepath.Join(nodeHome, "last_known_public_ip.json"))
		if err == nil {
			var ip string
			if json.Unmarshal(raw, &ip) != nil {
				ip = strings.Trim(strings.TrimSpace(string(raw)), "\"")
			}
			if net.ParseIP(ip) != nil {
				return ip
			}
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(raw))
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

// InitializeNodeIdentity fills the identity attached to exports. The public
// IP is best-effort and never blocks startup: egress-restricted validators
// cannot reach ipify at all.
func InitializeNodeIdentity(cfg MetricsConfig) error {
	ip := getPublicIP(cfg.NodeHome)
	if ip == "" {
		logger.Warning("Could not determine public IP (no %s/last_known_public_ip.json, ipify unreachable); server_ip attribute will be empty", cfg.NodeHome)
	}

	metricsMutex.Lock()
	defer metricsMutex.Unlock()

	nodeIdentity = NodeIdentity{
		ServerIP:         ip,
		Alias:            cfg.Alias,
		Chain:            cfg.Chain,
		ValidatorAddress: cfg.ValidatorAddress,
		IsValidator:      cfg.IsValidator,
	}

	return nil
}
