package tvremote

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	sync.Mutex
	pending      map[string]PairPending
	receipts     map[string]PairReceipt
	devices      map[string]Device
	pairAttempts int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		pending: map[string]PairPending{}, receipts: map[string]PairReceipt{}, devices: map[string]Device{},
	}
}
func (m *memoryStore) RateLimitPair(_ context.Context, _ string) (bool, error) {
	m.Lock()
	defer m.Unlock()
	m.pairAttempts++
	return m.pairAttempts <= 10, nil
}
func (m *memoryStore) StartPair(_ context.Context, code string, p PairPending) (bool, error) {
	m.Lock()
	defer m.Unlock()
	if _, exists := m.pending[code]; exists {
		return false, nil
	}
	m.pending[code] = p
	m.receipts[p.SessionHash] = PairReceipt{Status: "pending"}
	return true, nil
}
func (m *memoryStore) Pending(_ context.Context, code string) (PairPending, error) {
	m.Lock()
	defer m.Unlock()
	p, ok := m.pending[code]
	if !ok {
		return p, ErrNotFound
	}
	return p, nil
}
func (m *memoryStore) PairStatus(_ context.Context, session string) (PairReceipt, error) {
	m.Lock()
	defer m.Unlock()
	r, ok := m.receipts[session]
	if !ok {
		return r, ErrNotFound
	}
	return r, nil
}
func (m *memoryStore) Approve(_ context.Context, code string, p PairPending, d Device, r PairReceipt) (bool, error) {
	m.Lock()
	defer m.Unlock()
	current, ok := m.pending[code]
	if !ok || current != p {
		return false, nil
	}
	delete(m.pending, code)
	m.devices[d.ID] = d
	m.receipts[p.SessionHash] = r
	return true, nil
}
func (m *memoryStore) Get(_ context.Context, id string) (Device, error) {
	m.Lock()
	defer m.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return d, ErrNotFound
	}
	return d, nil
}
func (m *memoryStore) List(_ context.Context) ([]Device, error) {
	m.Lock()
	defer m.Unlock()
	result := []Device{}
	for _, d := range m.devices {
		result = append(result, d)
	}
	return result, nil
}
func (m *memoryStore) UpdateToken(_ context.Context, old, next Device) (bool, error) {
	m.Lock()
	defer m.Unlock()
	current, ok := m.devices[old.ID]
	if !ok || current != old {
		return false, nil
	}
	m.devices[old.ID] = next
	return true, nil
}
func (m *memoryStore) Remove(_ context.Context, id string) error {
	m.Lock()
	defer m.Unlock()
	delete(m.devices, id)
	return nil
}

type fakeSender struct {
	device  Device
	payload OpenPayload
	count   int
	err     error
}

func (f *fakeSender) Send(_ context.Context, d Device, p OpenPayload) (string, error) {
	f.device = d
	f.payload = p
	f.count++
	return "projects/test/messages/1", f.err
}

func postJSON(t *testing.T, handler http.Handler, route, body, auth string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if auth != "" {
		request.Header.Set("Authorization", "Bearer "+auth)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestTVPublicPairOpenAndRevoke(t *testing.T) {
	store := newMemoryStore()
	sender := &fakeSender{}
	s := &Service{Store: store, Sender: sender, Now: func() time.Time { return time.Unix(1700000000, 0) }}
	handler := PublicHandler(s)
	const firstToken = "valid_token_abcdefghijklmnopqrstuvwxyz_1"
	start := postJSON(t, handler, "/api/mcp?resource=tv&action=pair-start",
		`{"device_name":"Living room TV","fcm_token":"`+firstToken+`"}`, "")
	if start.Code != http.StatusOK {
		t.Fatalf("pair start HTTP %d: %s", start.Code, start.Body.String())
	}
	var pending struct{ Code, Session string }
	if err := json.Unmarshal(start.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if len(pending.Code) != 8 || len(pending.Session) != 43 {
		t.Fatalf("invalid credentials length: %+v", pending)
	}

	status := postJSON(t, handler, "/api/mcp?resource=tv&action=pair-status", `{"session":"`+pending.Session+`"}`, "")
	if !strings.Contains(status.Body.String(), `"status":"pending"`) {
		t.Fatal(status.Body.String())
	}
	approved, err := s.ApprovePair(context.Background(), PairInput{Code: pending.Code})
	if err != nil || !approved.Paired {
		t.Fatalf("approval failed: %v %+v", err, approved)
	}
	if approved.DeviceName != "Living room TV" {
		t.Fatalf("wrong name: %+v", approved)
	}
	if _, err := s.ApprovePair(context.Background(), PairInput{Code: pending.Code}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("single-use code must expire, got %v", err)
	}
	status = postJSON(t, handler, "/api/mcp?resource=tv&action=pair-status", `{"session":"`+pending.Session+`"}`, "")
	var receipt PairReceipt
	if err := json.Unmarshal(status.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "paired" || receipt.DeviceID != approved.DeviceID || len(receipt.DeviceSecret) != 43 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if strings.Contains(start.Body.String(), firstToken) || strings.Contains(status.Body.String(), firstToken) {
		t.Fatal("FCM token must not leak in public pairing responses")
	}
	if strings.Contains(mustJSON(t, approved), receipt.DeviceSecret) {
		t.Fatal("MCP approval exposed device secret")
	}

	badUpdate := postJSON(t, handler, "/api/mcp?resource=tv&action=device-token",
		`{"device_id":"`+receipt.DeviceID+`","fcm_token":"new_token_abcdefghijklmnopqrstuvwxyz_1"}`, "wrongsecret")
	if badUpdate.Code != http.StatusUnauthorized {
		t.Fatalf("bad secret gave %d", badUpdate.Code)
	}
	okUpdate := postJSON(t, handler, "/api/mcp?resource=tv&action=device-token",
		`{"device_id":"`+receipt.DeviceID+`","fcm_token":"new_token_abcdefghijklmnopqrstuvwxyz_1"}`, receipt.DeviceSecret)
	if okUpdate.Code != http.StatusOK {
		t.Fatalf("token refresh returned %d: %s", okUpdate.Code, okUpdate.Body.String())
	}
	result, err := s.OpenTitle(context.Background(), OpenInput{MediaType: "movie", IMDbID: "tt1375666"})
	if err != nil || result.Status != "accepted_by_fcm" || sender.count != 1 || sender.device.FCMToken != "new_token_abcdefghijklmnopqrstuvwxyz_1" {
		t.Fatalf("FCM command error: %v result: %+v sender: %+v", err, result, sender)
	}
	if sender.payload.IssuedAt != 1700000000 || sender.payload.CommandID == "" {
		t.Fatalf("invalid payload: %+v", sender.payload)
	}
	if strings.Contains(mustJSON(t, result), "fcm_token") {
		t.Fatal("MCP response contains FCM token")
	}

	devices, err := s.Devices(context.Background(), DevicesInput{})
	if err != nil || len(devices.Devices) != 1 || devices.Devices[0].DeviceID != approved.DeviceID {
		t.Fatalf("devices: %+v, %v", devices, err)
	}
	removed, err := s.Devices(context.Background(), DevicesInput{Action: "remove", DeviceID: approved.DeviceID})
	if err != nil || !removed.Removed || len(removed.Devices) != 0 {
		t.Fatalf("remove: %+v, %v", removed, err)
	}
	if _, err := s.OpenTitle(context.Background(), OpenInput{MediaType: "movie", IMDbID: "tt1375666"}); err == nil {
		t.Fatal("revoked TV must not receive a push")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTVSafetyAndValidation(t *testing.T) {
	for _, test := range []struct{ kind, id, expected string }{
		{"movie", "tt1375666", "stremio:///detail/movie/tt1375666/tt1375666"},
		{"series", "tt0903747", "stremio:///detail/series/tt0903747/"},
	} {
		actual, err := DetailLink(test.kind, test.id)
		if err != nil || actual != test.expected {
			t.Fatalf("link: %s %v", actual, err)
		}
	}
	for _, test := range []OpenInput{
		{MediaType: "film", IMDbID: "tt1375666"},
		{MediaType: "movie", IMDbID: "https://evil"},
		{MediaType: "movie", IMDbID: "tt123456/other"},
		{MediaType: "movie", IMDbID: "tt3"},
	} {
		if _, err := DetailLink(test.MediaType, test.IMDbID); err == nil {
			t.Fatalf("accepted: %+v", test)
		}
	}
	s := &Service{Store: newMemoryStore(), Sender: &fakeSender{}}
	for i := 0; i < 10; i++ {
		if _, _, err := s.StartPair(context.Background(), "Television", "valid_token_abcdefghijklmnopqrstuvwxyz", "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.StartPair(context.Background(), "Television", "valid_token_abcdefghijklmnopqrstuvwxyz", "127.0.0.1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}
	if _, err := s.ApprovePair(context.Background(), PairInput{Code: "1234"}); err == nil {
		t.Fatal("short code accepted")
	}
	if s := PublicHandler(nil); postJSON(t, s, "/api/mcp?resource=tv&action=pair-start", `{}`, "").Code != 404 {
		t.Fatal("disabled endpoint should 404")
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret("x")), []byte(hashSecret("y"))) == 1 {
		t.Fatal("hash collision")
	}
}
