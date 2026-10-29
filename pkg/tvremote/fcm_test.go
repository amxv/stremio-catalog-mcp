package tvremote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestFCMEmitsOnlyShortLivedTitleData(t *testing.T) {
	received := false
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-access-token" {
			t.Errorf("wrong FCM auth or method")
		}
		var body struct {
			Message struct {
				Token   string            `json:"token"`
				Data    map[string]string `json:"data"`
				Android struct {
					Priority string `json:"priority"`
					TTL      string `json:"ttl"`
				} `json:"android"`
				Notification json.RawMessage `json:"notification"`
			} `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Message.Token != "valid_token_abcdefghijklmnopqrstuvwxyz" {
			t.Errorf("wrong registration token")
		}
		if body.Message.Android.Priority != "HIGH" || body.Message.Android.TTL != "0s" {
			t.Errorf("push may be buffered or delayed")
		}
		if len(body.Message.Notification) > 0 {
			t.Error("notification payload bypasses FirebaseMessagingService when app is backgrounded")
		}
		for key, want := range map[string]string{
			"kind": "open_title", "media_type": "series", "imdb_id": "tt0903747",
			"command_id": "sample-command", "issued_at": "1700000000",
		} {
			if body.Message.Data[key] != want {
				t.Errorf("%s = %q want %q", key, body.Message.Data[key], want)
			}
		}
		if strings.Contains(string(mustJSON(t, body)), "autoPlay") {
			t.Error("must not autoplay")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/123"}`))
	}))
	defer endpoint.Close()
	sender := &FCMSender{
		ProjectID:          "test-project",
		ServiceAccountJSON: `{"type":"service_account","project_id":"test-project","client_email":"test@test-project.iam.gserviceaccount.com"}`,
		TokenSource:        oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-access-token", Expiry: time.Now().Add(time.Hour)}),
		Endpoint:           endpoint.URL, Client: endpoint.Client(),
	}
	name, err := sender.Send(context.Background(), Device{FCMToken: "valid_token_abcdefghijklmnopqrstuvwxyz"}, OpenPayload{
		MediaType: "series", IMDbID: "tt0903747", CommandID: "sample-command", IssuedAt: 1700000000,
	})
	if err != nil || name != "projects/test-project/messages/123" || !received {
		t.Fatalf("FCM failure %v message=%s received=%v", err, name, received)
	}
}

func TestFCMErrorNeverExposesProviderBody(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret-registration-token-must-not-appear", http.StatusForbidden)
	}))
	defer endpoint.Close()
	sender := &FCMSender{
		ProjectID:          "test-project",
		ServiceAccountJSON: `{"type":"service_account","project_id":"test-project","client_email":"test@test-project.iam.gserviceaccount.com"}`,
		TokenSource:        oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-access-token", Expiry: time.Now().Add(time.Hour)}),
		Endpoint:           endpoint.URL, Client: endpoint.Client(),
	}
	_, err := sender.Send(context.Background(), Device{FCMToken: "valid_token_abcdefghijklmnopqrstuvwxyz"}, OpenPayload{
		MediaType: "movie", IMDbID: "tt1375666",
	})
	if err == nil || strings.Contains(err.Error(), "secret-registration-token") {
		t.Fatalf("unsafe error %v", err)
	}
}
