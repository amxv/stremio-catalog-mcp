package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var transport = mcp.NewStreamableHTTPHandler(
	func(*http.Request) *mcp.Server {
		return makeServer()
	},
	&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
)

// Handler enforces the private MCP connection key on every request.
// A missing server key always fails closed, including during deployments.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Robots-Tag", "noindex")

		secret := strings.TrimSpace(os.Getenv("MCP_API_KEY"))
		if secret == "" {
			http.Error(w, "MCP endpoint not configured", http.StatusServiceUnavailable)
			return
		}

		keys := r.URL.Query()["key"]
		if len(keys) != 1 || !validKey(secret, keys[0]) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		transport.ServeHTTP(w, r)
	})
}

func validKey(expected, supplied string) bool {
	if supplied == "" {
		return false
	}
	want := sha256.Sum256([]byte(expected))
	got := sha256.Sum256([]byte(supplied))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}
