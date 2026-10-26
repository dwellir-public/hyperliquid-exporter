package monitors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
	"github.com/validaoxyz/hyperliquid-exporter/internal/utils"
)

const gossipConnectionsStream = "gossip_connections"

// gossipConnectionAllowlist maps every hl-node gossip_connections tag we know
// to a bounded event_type label. Tags outside it count as unknown so hl-node
// schema drift is visible without creating unbounded label values.
var gossipConnectionAllowlist = map[string]string{
	"verified gossip rpc":                                     "verified_gossip_rpc",
	"handle_stream_connection":                                "handle_stream_connection",
	"performing checks on stream":                             "performing_checks_on_stream",
	"got tcp greeting":                                        "got_tcp_greeting",
	"error checking connection":                               "error_checking_connection",
	"rejecting gossip stream because max peers reached":       "rejecting_gossip_stream_max_peers_reached",
	"closing gossip stream because no quorum yet":             "closing_gossip_stream_no_quorum_yet",
	"finished checks":                                         "finished_checks",
	"sending abci_state":                                      "sending_abci_state",
	"successfully sent abci_state":                            "successfully_sent_abci_state",
	"sending evm kvs":                                         "sending_evm_kvs",
	"dropping connection after sending abci state":            "dropping_connection_after_sending_abci_state",
	"marking node_ip as verified":                             "marking_node_ip_verified",
	"dropping connection":                                     "dropping_connection",
	"closing gossip stream because peer is already connected": "closing_gossip_stream_peer_already_connected",
}

type GossipConnectionsMonitor struct {
	config       *config.Config
	dir          string
	tail         tailState
	registerPeer func(string, peermon.PeerDirection)
	// perIP gates the per-peer_ip counters; they only make sense with
	// --peer-latency, where the peer set is bounded and curated
	perIP bool
}

func NewGossipConnectionsMonitor(cfg *config.Config, registerPeer func(string, peermon.PeerDirection)) *GossipConnectionsMonitor {
	return &GossipConnectionsMonitor{
		config:       cfg,
		dir:          filepath.Join(cfg.NodeHome, "data", "node_logs", "gossip_connections", "hourly"),
		registerPeer: registerPeer,
		perIP:        cfg.EnablePeerLatency,
	}
}

func StartGossipConnectionsMonitor(ctx context.Context, cfg *config.Config, errCh chan<- error, registerPeer func(string, peermon.PeerDirection)) {
	m := NewGossipConnectionsMonitor(cfg, registerPeer)

	if _, err := os.Stat(m.dir); os.IsNotExist(err) {
		logger.InfoComponent("gossip", "Gossip connections directory not found, monitoring disabled: %s", m.dir)
		metrics.SetSourceUp(gossipConnectionsStream, false)
		return
	}

	logger.InfoComponent("gossip", "Starting gossip connections monitor")
	m.monitor(ctx, errCh)
}

func (m *GossipConnectionsMonitor) monitor(ctx context.Context, errCh chan<- error) {
	ticker := time.NewTicker(gossipPollInterval)
	defer ticker.Stop()

	// seed from the current hour on startup for peer discovery; counters are
	// suppressed because Prometheus already recorded those samples
	if filePath, err := utils.LatestFile(m.dir); err == nil && filePath != "" {
		logger.InfoComponent("gossip", "First run: processing gossip connections file %s", filePath)
		m.tail.path = filePath
		if newOffset, err := m.processFile(filePath, 0, true); err != nil {
			logger.ErrorComponent("gossip", "Initial gossip connections processing error: %v", err)
		} else {
			m.tail.offset = newOffset
		}
	}

	for {
		select {
		case <-ctx.Done():
			logger.InfoComponent("gossip", "Gossip connections monitor shutting down")
			return
		case <-ticker.C:
			filePath, err := utils.LatestFile(m.dir)
			if err != nil {
				logger.ErrorComponent("gossip", "Error getting latest gossip connections file: %v", err)
				metrics.SetSourceUp(gossipConnectionsStream, false)
				continue
			}

			if filePath == "" {
				metrics.SetSourceUp(gossipConnectionsStream, false)
				continue
			}

			if filePath != m.tail.path {
				logger.InfoComponent("gossip", "Switching to new gossip connections file: %s", filePath)
			}

			err = m.tail.poll(filePath, func(path string, offset int64) (int64, error) {
				return m.processFile(path, offset, false)
			})
			metrics.SetSourceUp(gossipConnectionsStream, err == nil)
			if err != nil {
				logger.ErrorComponent("gossip", "Error processing gossip connections file: %v", err)
				select {
				case errCh <- fmt.Errorf("gossip connections monitor: %w", err):
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// gossipConnectionEvent is one parsed gossip_connections line.
type gossipConnectionEvent struct {
	tag     string
	event   string // allowlisted label, or "other"
	known   bool
	payload []json.RawMessage // inner array including the tag at index 0
}

// parseGossipConnectionLine validates the record envelope and, for known
// tags, the payload shape. stage is non-empty when the line is rejected.
func parseGossipConnectionLine(line []byte) (ev gossipConnectionEvent, stage string) {
	var outer []json.RawMessage
	if err := json.Unmarshal(line, &outer); err != nil {
		return ev, "json"
	}
	if len(outer) != 2 {
		return ev, "shape"
	}
	var ts string
	if err := unmarshalRequiredJSON(outer[0], &ts); err != nil {
		return ev, "timestamp"
	}
	if _, ok := parseVisorTime(ts); !ok {
		return ev, "timestamp"
	}
	var inner []json.RawMessage
	if err := json.Unmarshal(outer[1], &inner); err != nil || len(inner) == 0 {
		return ev, "shape"
	}
	if err := unmarshalRequiredJSON(inner[0], &ev.tag); err != nil {
		return ev, "shape"
	}
	ev.payload = inner
	ev.event, ev.known = gossipConnectionAllowlist[ev.tag]
	if !ev.known {
		ev.event = "other"
		return ev, ""
	}
	if !validKnownGossipPayload(ev.tag, inner) {
		return ev, "payload"
	}
	return ev, ""
}

func validKnownGossipPayload(tag string, inner []json.RawMessage) bool {
	switch tag {
	case "handle_stream_connection":
		// testnet also emits the endpoint without a stream field
		if len(inner) == 2 {
			var endpoint string
			return unmarshalRequiredJSON(inner[1], &endpoint) == nil && strings.TrimSpace(endpoint) != ""
		}
		fallthrough
	case "performing checks on stream":
		if len(inner) != 3 {
			return false
		}
		var endpoint, stream string
		return unmarshalRequiredJSON(inner[1], &endpoint) == nil && unmarshalRequiredJSON(inner[2], &stream) == nil
	case "closing gossip stream because no quorum yet":
		// newer builds emit a string plus an array; older ones the
		// IP-object/bool form; current mainnet the IP object plus an
		// unsigned integer. Validation only, never a label
		return validGossipIPFlagPayload(inner, false) || validGossipStringArrayPayload(inner) ||
			validGossipIPUintPayload(inner)
	case "successfully sent abci_state":
		// current testnet appends a nonnegative number of unknown units
		if len(inner) == 4 {
			var value float64
			return validGossipIPFlagPayload(inner[:3], false) &&
				unmarshalRequiredJSON(inner[3], &value) == nil && !math.Signbit(value)
		}
		return validGossipIPFlagPayload(inner, false)
	case "finished checks", "sending abci_state":
		return validGossipIPFlagPayload(inner, false)
	case "dropping connection after sending abci state":
		// third field is a bool on older builds and a short string on current
		// mainnet, which also emits the IP object alone
		if len(inner) == 2 {
			return rawExactIPObject(inner[1])
		}
		return validGossipIPBoolOrStringPayload(inner)
	case "sending evm kvs", "marking node_ip as verified",
		"closing gossip stream because peer is already connected":
		// current mainnet emits object-only; older builds add a trailing bool
		return validGossipIPFlagPayload(inner, true)
	case "dropping connection":
		// current mainnet emits the IP object plus a reason string
		if len(inner) == 3 {
			var reason string
			return rawExactIPObject(inner[1]) &&
				unmarshalRequiredJSON(inner[2], &reason) == nil && strings.TrimSpace(reason) != ""
		}
		return len(inner) == 5
	case "got tcp greeting":
		if len(inner) == 2 {
			return rawExactIPObject(inner[1])
		}
		if len(inner) != 3 || !rawExactIPObject(inner[1]) {
			return false
		}
		var flag bool
		return unmarshalRequiredJSON(inner[2], &flag) == nil
	case "rejecting gossip stream because max peers reached":
		// current mainnet emits the IP object plus an unsigned integer
		if validGossipIPUintPayload(inner) {
			return true
		}
		if len(inner) == 2 {
			return rawJSONObject(inner[1])
		}
		if len(inner) != 3 || !rawJSONArray(inner[2]) {
			return false
		}
		var message string
		return unmarshalRequiredJSON(inner[1], &message) == nil
	case "error checking connection":
		if len(inner) == 2 {
			return rawJSONObject(inner[1])
		}
		if len(inner) != 3 {
			return false
		}
		var endpoint, detail string
		return unmarshalRequiredJSON(inner[1], &endpoint) == nil && unmarshalRequiredJSON(inner[2], &detail) == nil
	default:
		return len(inner) == 2 && rawJSONObject(inner[1])
	}
}

func validGossipIPFlagPayload(inner []json.RawMessage, allowObjectOnly bool) bool {
	if len(inner) == 2 {
		return allowObjectOnly && rawExactIPObject(inner[1])
	}
	if len(inner) != 3 || !rawExactIPObject(inner[1]) {
		return false
	}
	var flag bool
	return unmarshalRequiredJSON(inner[2], &flag) == nil
}

func validGossipIPUintPayload(inner []json.RawMessage) bool {
	if len(inner) != 3 || !rawExactIPObject(inner[1]) {
		return false
	}
	var value uint64
	return unmarshalRequiredJSON(inner[2], &value) == nil
}

func validGossipIPBoolOrStringPayload(inner []json.RawMessage) bool {
	if len(inner) != 3 || !rawExactIPObject(inner[1]) {
		return false
	}
	var flag bool
	if unmarshalRequiredJSON(inner[2], &flag) == nil {
		return true
	}
	var detail string
	return unmarshalRequiredJSON(inner[2], &detail) == nil && strings.TrimSpace(detail) != ""
}

func validGossipStringArrayPayload(inner []json.RawMessage) bool {
	if len(inner) != 3 || !rawJSONArray(inner[2]) {
		return false
	}
	var detail string
	return unmarshalRequiredJSON(inner[1], &detail) == nil && strings.TrimSpace(detail) != ""
}

// rawExactIPObject accepts exactly {"Ip": "<nonempty>"}. It walks tokens
// instead of decoding into a map, which would silently keep the last of
// duplicate keys.
func rawExactIPObject(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	if tok, err := dec.Token(); err != nil || tok != "Ip" {
		return false
	}
	var ip string
	if err := dec.Decode(&ip); err != nil || strings.TrimSpace(ip) == "" {
		return false
	}
	tok, err := dec.Token()
	return err == nil && tok == json.Delim('}')
}

// processFile tails filePath from offset. With seeding set, counters are not
// incremented but peers are still registered (startup replay).
func (m *GossipConnectionsMonitor) processFile(filePath string, offset int64, seeding bool) (int64, error) {
	if filePath == "" {
		return offset, fmt.Errorf("empty file path")
	}

	sampled := false
	newOffset, err := readCommittedLines(filePath, offset, func(line []byte) {
		ev, stage := parseGossipConnectionLine(line)
		if stage != "" {
			if !seeding {
				metrics.IncrementParseErrors(gossipConnectionsStream, stage)
			}
			return
		}
		sampled = true
		if !seeding {
			if !ev.known {
				metrics.IncrementGossipUnknownEvent()
				logger.DebugComponent("gossip", "Unknown gossip_connections event tag %q", ev.tag)
			}
			metrics.IncrementGossipEvent(ev.event)
		}
		m.discover(ev, m.perIP && !seeding)
	})
	if err != nil {
		return offset, fmt.Errorf("failed to tail gossip connections file: %w", err)
	}
	if sampled {
		metrics.MarkSourceSample(gossipConnectionsStream, time.Now())
	}

	return newOffset, nil
}

// discover registers the peer carried by the two identity-bearing events and
// publishes their per-IP counters when count is set.
func (m *GossipConnectionsMonitor) discover(ev gossipConnectionEvent, count bool) {
	switch ev.tag {
	case "handle_stream_connection":
		var ipPort, connType string
		_ = json.Unmarshal(ev.payload[1], &ipPort)
		if len(ev.payload) > 2 {
			_ = json.Unmarshal(ev.payload[2], &connType)
		}
		peerIP, _, err := net.SplitHostPort(ipPort)
		if err != nil {
			peerIP = ipPort
		}
		if peerIP == "" {
			return
		}
		if count {
			metrics.IncrementStreamConnections(peerIP, connType)
		}
		if m.registerPeer != nil {
			m.registerPeer(peerIP, connTypeToDirection(connType))
		}

	case "verified gossip rpc":
		var peer struct {
			IP string `json:"Ip"`
		}
		if err := json.Unmarshal(ev.payload[1], &peer); err != nil || peer.IP == "" {
			return
		}
		if count {
			metrics.IncrementVerifications(peer.IP)
		}
		if m.registerPeer != nil {
			m.registerPeer(peer.IP, peermon.Unknown)
		}
	}
}

func connTypeToDirection(connType string) peermon.PeerDirection {
	switch strings.ToLower(connType) {
	case "inbound":
		return peermon.Inbound
	case "outbound":
		return peermon.Outbound
	default:
		return peermon.Unknown
	}
}
