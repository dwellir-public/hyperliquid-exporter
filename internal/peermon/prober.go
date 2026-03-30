package peermon

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"
)

var candidatePorts = []int{
	3001, 3002, 443, 80,
	4000, 4001, 4002, 4003, 4004, 4005,
	4006, 4007, 4008, 4009, 4010,
}

var probeAttemptTimeout = 3 * time.Second

var dialAddr = func(ctx context.Context, addr string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", addr)
}

// ProbeResult holds the outcome of a single TCP probe.
type ProbeResult struct {
	IP        string
	Port      int
	Latency   time.Duration
	Reachable bool
}

// Probe attempts the preferred port first, then probes the remaining
// ports 4000-4010 concurrently, returning the first success.
func Probe(ctx context.Context, ip string, preferredPort int) ProbeResult {
	if preferredPort > 0 {
		if result, ok := tryPort(ctx, ip, preferredPort); ok {
			return result
		}
	}

	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ports := fallbackPorts(preferredPort)
	results := make(chan ProbeResult, len(ports))

	var wg sync.WaitGroup
	for _, port := range ports {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			if result, ok := tryPort(probeCtx, ip, port); ok {
				select {
				case results <- result:
					cancel()
				case <-probeCtx.Done():
				}
			}
		}(port)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case result := <-results:
		<-done
		return result
	case <-done:
		return ProbeResult{IP: ip}
	case <-ctx.Done():
		<-done
		return ProbeResult{IP: ip}
	}
}

func tryPort(ctx context.Context, ip string, port int) (ProbeResult, bool) {
	if ctx.Err() != nil {
		return ProbeResult{IP: ip}, false
	}

	attemptCtx, cancel := context.WithTimeout(ctx, probeAttemptTimeout)
	defer cancel()

	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	start := time.Now()

	conn, err := dialAddr(attemptCtx, addr)
	if err != nil {
		return ProbeResult{IP: ip}, false
	}

	latency := time.Since(start)
	_ = conn.Close()
	return ProbeResult{
		IP:        ip,
		Port:      port,
		Latency:   latency,
		Reachable: true,
	}, true
}

func fallbackPorts(preferredPort int) []int {
	ports := make([]int, 0, len(candidatePorts))
	for _, port := range candidatePorts {
		if port != preferredPort {
			ports = append(ports, port)
		}
	}
	return ports
}
