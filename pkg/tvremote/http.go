package tvremote

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

//go:embed setup.html setup.js setup.css
var setupAssets embed.FS

// PublicHandler exposes only the narrowly scoped, rate-limited pairing and
// authenticated token-rotation routes, plus a separate authenticated owner
// setup UI. It never accepts an open/play command. Sending titles requires
// the private MCP key via tv_open_title.
func PublicHandler(service *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if service == nil {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("action") {
		case "setup", "setup-js", "setup-css":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "GET required")
				return
			}
			name := "setup.html"
			contentType := "text/html; charset=utf-8"
			switch r.URL.Query().Get("action") {
			case "setup-js":
				name, contentType = "setup.js", "text/javascript; charset=utf-8"
			case "setup-css":
				name, contentType = "setup.css", "text/css; charset=utf-8"
			}
			data, err := setupAssets.ReadFile(name)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "setup page unavailable")
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		case "admin":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "POST required")
				return
			}
			if len(service.SetupKey) < 32 {
				writeError(w, http.StatusServiceUnavailable, "TV_SETUP_KEY is not configured")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host {
				writeError(w, http.StatusForbidden, "cross-origin requests are not allowed")
				return
			}
			const prefix = "Bearer "
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, prefix) {
				writeError(w, http.StatusUnauthorized, "setup credential required")
				return
			}
			supplied := sha256.Sum256([]byte(strings.TrimPrefix(auth, prefix)))
			expected := sha256.Sum256([]byte(service.SetupKey))
			if subtle.ConstantTimeCompare(supplied[:], expected[:]) != 1 {
				writeError(w, http.StatusUnauthorized, "invalid setup credential")
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "application/json required")
				return
			}
			var request struct {
				Action   string `json:"action"`
				Code     string `json:"code,omitempty"`
				DeviceID string `json:"device_id,omitempty"`
			}
			if !decodeRequest(w, r, &request) {
				return
			}
			switch request.Action {
			case "list":
				if request.Code != "" || request.DeviceID != "" {
					writeError(w, http.StatusBadRequest, "unexpected fields")
					return
				}
			case "pair":
				if request.DeviceID != "" {
					writeError(w, http.StatusBadRequest, "unexpected device_id")
					return
				}
				result, err := service.ApprovePair(r.Context(), PairInput{Code: request.Code})
				if err != nil {
					writeServiceError(w, err)
					return
				}
				_ = result
			case "remove":
				if request.Code != "" {
					writeError(w, http.StatusBadRequest, "unexpected code")
					return
				}
				if err := service.RevokeDevice(r.Context(), request.DeviceID); err != nil {
					writeServiceError(w, err)
					return
				}
			default:
				writeError(w, http.StatusBadRequest, "action must be list, pair or remove")
				return
			}
			devices, err := service.ListDevices(r.Context())
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"devices": devices, "max_devices": maxDevices})
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
	case errors.Is(err, ErrAlreadyPaired):
		writeError(w, http.StatusConflict, "one TV is already paired; revoke it first")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid pairing or token request")
	default:
		// Never return provider responses, credentials, or Redis details publicly.
		writeError(w, http.StatusServiceUnavailable, "TV pairing service unavailable or request invalid")
	}
}
