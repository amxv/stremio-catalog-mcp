package tvremote

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

// PublicHandler exposes only the narrowly scoped, rate-limited pairing and
// authenticated token-rotation routes. It never accepts an open/play command.
// Sending titles requires the private MCP key, via tv_open_title.
func PublicHandler(service *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if service == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "application/json required")
			return
		}
		switch r.URL.Query().Get("action") {
		case "pair-start":
			var req struct {
				Name  string `json:"device_name"`
				Token string `json:"fcm_token"`
			}
			if !decodeRequest(w, r, &req) {
				return
			}
			code, session, err := service.StartPair(r.Context(), req.Name, req.Token, clientAddress(r))
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"code": code, "session": session, "expires_in_seconds": int(pairTTL.Seconds())})
		case "pair-status":
			var req struct {
				Session string `json:"session"`
			}
			if !decodeRequest(w, r, &req) {
				return
			}
			receipt, err := service.PairStatus(r.Context(), req.Session)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, receipt)
		case "device-token":
			var req struct {
				DeviceID string `json:"device_id"`
				Token    string `json:"fcm_token"`
			}
			if !decodeRequest(w, r, &req) {
				return
			}
			const prefix = "Bearer "
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, prefix) {
				writeError(w, http.StatusUnauthorized, "device authentication required")
				return
			}
			if err := service.UpdateDeviceToken(r.Context(), req.DeviceID, strings.TrimPrefix(auth, prefix), req.Token); err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
		default:
			http.NotFound(w, r)
		}
	})
}

func decodeRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	reader := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	reader.DisallowUnknownFields()
	if err := reader.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	var extra any
	if err := reader.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return false
	}
	return true
}

func clientAddress(r *http.Request) string {
	// Vercel injects X-Vercel-Forwarded-For. If absent, use the socket peer.
	// Rate limits are also global, so a spoofed header cannot bypass the cap.
	candidate := strings.TrimSpace(strings.Split(r.Header.Get("X-Vercel-Forwarded-For"), ",")[0])
	if candidate == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			candidate = host
		} else {
			candidate = r.RemoteAddr
		}
	}
	if net.ParseIP(candidate) == nil {
		return "unknown"
	}
	return candidate
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "pairing request not found or expired")
	case errors.Is(err, ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "invalid device authentication")
	case errors.Is(err, ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "too many pairing requests; retry later")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid pairing or token request")
	default:
		// Never return provider responses, credentials, or Redis details publicly.
		writeError(w, http.StatusServiceUnavailable, "TV pairing service unavailable or request invalid")
	}
}
