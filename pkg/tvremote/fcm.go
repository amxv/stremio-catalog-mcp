package tvremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const messagingScope = "https://www.googleapis.com/auth/firebase.messaging"

var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,29}$`)

// FCMSender sends only ephemeral, data-only pushes. No Firebase database,
// Cloud Functions, device polling, or additional always-on service is needed.
type FCMSender struct {
	ProjectID          string
	ServiceAccountJSON string
	Client             *http.Client
	TokenSource        oauth2.TokenSource // used by tests; production uses service-account OAuth
	Endpoint           string             // used by tests; production always uses Google's FCM v1 endpoint
}

func (s *FCMSender) Validate() error {
	if s == nil || !projectIDPattern.MatchString(s.ProjectID) || s.ServiceAccountJSON == "" {
		return errors.New("TV_OPEN_ENABLED requires FCM_PROJECT_ID and FCM_SERVICE_ACCOUNT_JSON")
	}
	var details struct {
		Type        string `json:"type"`
		ProjectID   string `json:"project_id"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal([]byte(s.ServiceAccountJSON), &details); err != nil {
		return errors.New("invalid FCM service account JSON")
	}
	if details.Type != "service_account" || details.ProjectID != s.ProjectID || details.ClientEmail == "" {
		return errors.New("FCM service account must belong to FCM_PROJECT_ID")
	}
	if s.TokenSource == nil {
		if _, err := google.JWTConfigFromJSON([]byte(s.ServiceAccountJSON), messagingScope); err != nil {
			return errors.New("FCM service account is missing valid signing credentials")
		}
	}
	return nil
}

func (s *FCMSender) Send(ctx context.Context, device Device, payload OpenPayload) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	if !validFCMToken(device.FCMToken) {
		return "", errors.New("paired TV has an invalid FCM token; re-pair")
	}
	if _, err := EpisodeLink(payload.MediaType, payload.IMDbID, payload.Season, payload.Episode); err != nil {
		return "", err
	}
	source := s.TokenSource
	if source == nil {
		config, err := google.JWTConfigFromJSON([]byte(s.ServiceAccountJSON), messagingScope)
		if err != nil {
			return "", fmt.Errorf("invalid FCM OAuth credentials: %w", err)
		}
		source = config.TokenSource(ctx)
	}
	token, err := source.Token()
	if err != nil {
		return "", fmt.Errorf("FCM OAuth failed: %w", err)
	}
	if !token.Valid() {
		return "", errors.New("FCM OAuth returned invalid access token")
	}
	data := map[string]string{
		"kind": "open_title", "media_type": payload.MediaType,
		"imdb_id": payload.IMDbID, "command_id": payload.CommandID,
		"issued_at": strconv.FormatInt(payload.IssuedAt, 10),
	}
	if payload.Season != nil && payload.Episode != nil {
		data["season"] = strconv.Itoa(*payload.Season)
		data["episode"] = strconv.Itoa(*payload.Episode)
	}
	body := map[string]any{
		"message": map[string]any{
			"token": device.FCMToken,
			"data":  data,
			"android": map[string]any{
				"priority": "HIGH",
				"ttl":      "0s", // Never surprise the user with an old title after TV reconnects.
			},
		},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	endpoint := "https://fcm.googleapis.com/v1/projects/" + s.ProjectID + "/messages:send"
	if s.Endpoint != "" {
		endpoint = s.Endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("FCM send failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Never echo upstream bodies: provider diagnostics can contain tokens.
		if response.StatusCode == http.StatusNotFound {
			return "", errors.New("FCM rejected this device registration; re-pair the TV")
		}
		return "", fmt.Errorf("FCM rejected push with HTTP %d", response.StatusCode)
	}
	var result struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid FCM response: %w", err)
	}
	if !strings.HasPrefix(result.Name, "projects/"+s.ProjectID+"/messages/") {
		return "", errors.New("FCM returned unexpected message reference")
	}
	return result.Name, nil
}
