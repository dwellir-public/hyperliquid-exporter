package metrics_test

import (
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

// freeAddr returns a loopback address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

func TestStartPrometheusServer_Addr(t *testing.T) {
	addr := freeAddr(t)
	require.NoError(t, metrics.StartPrometheusServer(t.Context(), addr))

	resp, err := http.Get("http://" + addr + "/health")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK\n", string(body))

	// a taken address must fail startup, not leave a metric-less process
	assert.Error(t, metrics.StartPrometheusServer(t.Context(), addr))
}
