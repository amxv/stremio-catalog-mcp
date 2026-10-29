package tvremote

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

type memoryStore struct {
	sync.Mutex
	pending      map[string]PairPending
	receipts     map[string]PairReceipt
	commands     map[string]CommandReceipt
	devices      map[string]Device
	pairAttempts int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		pending: map[string]PairPending{}, receipts: map[string]PairReceipt{}, devices: map[string]Device{}, commands: map[string]CommandReceipt{},
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
	if len(m.devices) != 0 {
		return false, ErrAlreadyPaired
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

func (m *memoryStore) ReserveCommand(_ context.Context, id, deviceID string) error {
	m.Lock()
	defer m.Unlock()
	if _, exists := m.commands[id]; exists {
		return errors.New("duplicate command")
	}
	m.commands[id] = CommandReceipt{DeviceID: deviceID, Status: "pending"}
	return nil
}

func (m *memoryStore) CommandReceipt(_ context.Context, id string) (CommandReceipt, error) {
	m.Lock()
	defer m.Unlock()
	result, ok := m.commands[id]
	if !ok {
		return CommandReceipt{}, ErrNotFound
	}
	return result, nil
}

func (m *memoryStore) AcknowledgeCommand(_ context.Context, id, deviceID, status, when string) (bool, error) {
	m.Lock()
	defer m.Unlock()
	r, ok := m.commands[id]
	if !ok || r.DeviceID != deviceID || r.Status != "pending" {
		return false, nil
	}
	r.Status, r.UpdatedAt = status, when
	m.commands[id] = r
	return true, nil
}

func (m *memoryStore) CancelCommand(_ context.Context, id string) error {
	m.Lock()
	defer m.Unlock()
	delete(m.commands, id)
	return nil
}

type fakeSender struct {
	device  Device
	payload OpenPayload
	count   int
	err     error
	onSend  func(Device, OpenPayload)
}

func (f *fakeSender) Send(_ context.Context, d Device, p OpenPayload) (string, error) {
	f.device = d
	f.payload = p
	f.count++
	if f.onSend != nil {
		f.onSend(d, p)
	}
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
	s := &Service{Store: store, Sender: sender, ReceiptWait: -1, SetupKey: "test-only-private-administration-secret-123456", Now: func() time.Time { return time.Unix(1700000000, 0) }}
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
	pairResponse := postJSON(t, handler, "/api/mcp?resource=tv&action=admin",
		`{"action":"pair","code":"`+pending.Code+`"}`, s.SetupKey)
	if pairResponse.Code != http.StatusOK {
		t.Fatalf("admin pair HTTP %d: %s", pairResponse.Code, pairResponse.Body.String())
	}
	var pairedResult struct {
		Devices    []DeviceSummary `json:"devices"`
		MaxDevices int             `json:"max_devices"`
	}
	if err := json.Unmarshal(pairResponse.Body.Bytes(), &pairedResult); err != nil {
		t.Fatal(err)
	}
	if len(pairedResult.Devices) != 1 || pairedResult.MaxDevices != 1 || pairedResult.Devices[0].Name != "Living room TV" {
		t.Fatalf("wrong device state: %+v", pairedResult)
	}
	approved := pairedResult.Devices[0]
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
	if strings.Contains(pairResponse.Body.String(), receipt.DeviceSecret) || strings.Contains(pairResponse.Body.String(), firstToken) {
		t.Fatal("admin approval exposed device secret or FCM token")
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

	devices, err := s.ListDevices(context.Background())
	if err != nil || len(devices) != 1 || devices[0].DeviceID != approved.DeviceID {
		t.Fatalf("devices: %+v, %v", devices, err)
	}
	removeResponse := postJSON(t, handler, "/api/mcp?resource=tv&action=admin", `{"action":"remove","device_id":"`+approved.DeviceID+`"}`, s.SetupKey)
	if removeResponse.Code != http.StatusOK || !strings.Contains(removeResponse.Body.String(), `"devices":[]`) {
		t.Fatalf("remove: %d %s", removeResponse.Code, removeResponse.Body.String())
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

func testInt(n int) *int { return &n }

func TestSeriesEpisodeLinks(t *testing.T) {
	for _, test := range []struct {
		typ, id         string
		season, episode *int
		want            string
	}{
		{"movie", "tt1375666", nil, nil, "stremio:///detail/movie/tt1375666/tt1375666"},
		{"series", "tt0903747", nil, nil, "stremio:///detail/series/tt0903747/"},
		{"series", "tt0903747", testInt(3), testInt(4), "stremio:///detail/series/tt0903747/tt0903747:3:4"},
		{"series", "tt0903747", testInt(0), testInt(1), "stremio:///detail/series/tt0903747/tt0903747:0:1"},
	} {
		got, err := EpisodeLink(test.typ, test.id, test.season, test.episode)
		if err != nil || got != test.want {
			t.Errorf("EpisodeLink got %q, %v; want %q", got, err, test.want)
		}
		if strings.Contains(got, "autoPlay") {
			t.Fatal("episode link must never autoplay")
		}
	}
	for _, test := range []struct {
		typ             string
		season, episode *int
	}{
		{"series", testInt(1), nil}, {"series", nil, testInt(1)},
		{"movie", testInt(1), testInt(1)}, {"series", testInt(-1), testInt(1)},
		{"series", testInt(1), testInt(0)}, {"series", testInt(101), testInt(1)},
		{"series", testInt(1), testInt(1001)},
	} {
		if _, err := EpisodeLink(test.typ, "tt0903747", test.season, test.episode); err == nil {
			t.Errorf("accepted invalid episode %+v", test)
		}
	}
}

func TestDeliveryReceiptsAreAuthenticatedAndHonest(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	key, err := secret()
	if err != nil {
		t.Fatal(err)
	}
	id := "tv_0123456789abcdef0123456789abcdef"
	store.devices[id] = Device{ID: id, Name: "TV", FCMToken: "valid_token_abcdefghijklmnopqrstuvwxyz", SecretHash: hashSecret(key)}
	sender := &fakeSender{}
	s := &Service{Store: store, Sender: sender, ReceiptWait: 20 * time.Millisecond, SetupKey: "owner-super-secret-1234567890123456789"}
	handler := PublicHandler(s)
	sender.onSend = func(_ Device, p OpenPayload) {
		if p.Season == nil || *p.Season != 3 || p.Episode == nil || *p.Episode != 4 {
			t.Errorf("missing episode selection: %+v", p)
		}
		url := "/api/mcp?resource=tv&action=device-receipt"
		body := fmt.Sprintf(`{"device_id":%q,"command_id":%q,"status":"launch_requested"}`, id, p.CommandID)
		for _, test := range []struct {
			label, body, auth string
			code              int
		}{
			{"missing authentication", body, "", 401},
			{"wrong credential", body, "wrong", 401},
			{"wrong TV", strings.Replace(body, id, "tv_ffffffffffffffffffffffffffffffff", 1), key, 401},
			{"unknown command", strings.Replace(body, p.CommandID, "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", 1), key, 404},
			{"unsupported status", strings.Replace(body, "launch_requested", "opened", 1), key, 400},
			{"valid receipt", body, key, 200},
			{"duplicate receipt", body, key, 404},
		} {
			resp := postJSON(t, handler, url, test.body, test.auth)
			if resp.Code != test.code {
				t.Errorf("%s status %d, want %d: %s", test.label, resp.Code, test.code, resp.Body.String())
			}
		}
	}
	result, err := s.OpenTitle(ctx, OpenInput{MediaType: "series", IMDbID: "tt0903747", Season: testInt(3), Episode: testInt(4)})
	if err != nil || result.Status != "accepted_by_fcm" || result.DeliveryStatus != "launch_requested" {
		t.Fatalf("receipt missing from MCP result: %+v err=%v", result, err)
	}
	if len(result.CommandID) != 43 || !strings.Contains(result.Note, "not confirmed") || result.Season == nil || *result.Season != 3 {
		t.Fatalf("result should identify selected episode without overclaiming: %+v", result)
	}
	if strings.Contains(mustJSON(t, result), key) || strings.Contains(mustJSON(t, result), store.devices[id].FCMToken) {
		t.Fatal("MCP result disclosed credentials")
	}
	admin := postJSON(t, handler, "/api/mcp?resource=tv&action=admin", fmt.Sprintf(`{"action":"delivery","command_id":%q}`, result.CommandID), s.SetupKey)
	if admin.Code != 200 || !strings.Contains(admin.Body.String(), "launch_requested") {
		t.Fatalf("admin delivery receipt unavailable: %d %s", admin.Code, admin.Body.String())
	}
	if strings.Contains(admin.Body.String(), key) || strings.Contains(admin.Body.String(), id) {
		t.Fatal("admin receipt leaked device ID or secret")
	}
	// An unauthenticated client cannot inspect delivery status either.
	if noAuth := postJSON(t, handler, "/api/mcp?resource=tv&action=admin", fmt.Sprintf(`{"action":"delivery","command_id":%q}`, result.CommandID), ""); noAuth.Code != 401 {
		t.Fatal("delivery status public")
	}
}

func TestDeliveryTimeoutAndSendFailure(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	id := "tv_0123456789abcdef0123456789abcdef"
	store.devices[id] = Device{ID: id, Name: "TV", FCMToken: "valid_token_abcdefghijklmnopqrstuvwxyz"}
	sender := &fakeSender{}
	s := &Service{Store: store, Sender: sender, ReceiptWait: 5 * time.Millisecond}
	result, err := s.OpenTitle(ctx, OpenInput{MediaType: "movie", IMDbID: "tt1375666"})
	if err != nil || result.Status != "accepted_by_fcm" || result.DeliveryStatus != "not_confirmed" {
		t.Fatalf("unconfirmed delivery must not be claimed: %+v %v", result, err)
	}
	receipt, err := store.CommandReceipt(ctx, result.CommandID)
	if err != nil || receipt.Status != "pending" {
		t.Fatalf("missing pending command: %+v %v", receipt, err)
	}
	sender.err = errors.New("provider failure")
	_, err = s.OpenTitle(ctx, OpenInput{MediaType: "movie", IMDbID: "tt1375666"})
	if err == nil {
		t.Fatal("provider failure not returned")
	}
	store.Lock()
	count := len(store.commands)
	store.Unlock()
	if count != 1 {
		t.Fatalf("failed FCM push left stale command records: %d", count)
	}
}

func TestTVSetupAdminAuthenticationAndAssets(t *testing.T) {
	s := &Service{Store: newMemoryStore(), Sender: &fakeSender{}, SetupKey: "test-only-43-character-owner-secret-example-7777"}
	handler := PublicHandler(s)
	for _, test := range []struct {
		name, action, asset, contentType string
	}{
		{"html", "setup", "TV setup", "text/html"},
		{"css", "setup-css", ".panel", "text/css"},
		{"js", "setup-js", "/tv/admin", "text/javascript"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/mcp?resource=tv&action="+test.action, nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), test.asset) ||
				!strings.HasPrefix(recorder.Header().Get("Content-Type"), test.contentType) {
				t.Fatalf("unexpected UI response %d, content-type %s", recorder.Code, recorder.Header().Get("Content-Type"))
			}
			if !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "script-src 'self'") ||
				recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Frame-Options") != "DENY" {
				t.Fatal("admin UI missing security headers")
			}
		})
	}
	const route = "/api/mcp?resource=tv&action=admin"
	for _, tc := range []struct {
		label, key string
		status     int
	}{
		{"missing key", "", http.StatusUnauthorized},
		{"wrong key", "not-it", http.StatusUnauthorized},
		{"valid key", s.SetupKey, http.StatusOK},
	} {
		t.Run(tc.label, func(t *testing.T) {
			response := postJSON(t, handler, route, `{"action":"list"}`, tc.key)
			if response.Code != tc.status {
				t.Fatalf("%s: %d: %s", tc.label, response.Code, response.Body.String())
			}
			if tc.status == 200 && !strings.Contains(response.Body.String(), `"max_devices":1`) {
				t.Fatalf("single-TV constraint not reported: %s", response.Body.String())
			}
		})
	}
	crossSite := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{"action":"list"}`))
	crossSite.Header.Set("Content-Type", "application/json")
	crossSite.Header.Set("Authorization", "Bearer "+s.SetupKey)
	crossSite.Header.Set("Origin", "https://evil.example")
	crossRecorder := httptest.NewRecorder()
	handler.ServeHTTP(crossRecorder, crossSite)
	if crossRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin admin request accepted: %d", crossRecorder.Code)
	}
	if response := postJSON(t, handler, route, `{"action":"play"}`, s.SetupKey); response.Code != 400 {
		t.Fatal("admin endpoint must not support opening or playing titles")
	}
	if response := postJSON(t, handler, route, `{"action":"remove","device_id":"invalid"}`, s.SetupKey); response.Code != 400 {
		t.Fatal("invalid revoke request should be rejected")
	}
	s.SetupKey = ""
	if response := postJSON(t, handler, route, `{"action":"list"}`, "something"); response.Code != 503 {
		t.Fatal("missing admin credential must fail closed")
	}
	for _, route := range []string{
		"/api/mcp?resource=tv&action=setup",
		"/api/mcp?resource=tv&action=admin",
	} {
		if response := postJSON(t, PublicHandler(nil), route, `{"action":"list"}`, s.SetupKey); response.Code != 404 {
			t.Fatalf("disabled TV integration must return 404 for %s", route)
		}
	}
}

func TestSingleTVPairingIsAtomic(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s := &Service{Store: store, Sender: &fakeSender{}}
	codes := make([]string, 2)
	for i := range codes {
		code, _, err := s.StartPair(ctx, "TV", "valid_token_abcdefghijklmnopqrstuvwxyz", "192.0.2.2")
		if err != nil {
			t.Fatal(err)
		}
		codes[i] = code
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, code := range codes {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			_, err := s.ApprovePair(ctx, PairInput{Code: code})
			results <- err
		}(code)
	}
	wg.Wait()
	close(results)
	accepted, blocked := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrAlreadyPaired) {
			blocked++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if accepted != 1 || blocked != 1 {
		t.Fatalf("pair approvals: accepted=%d,blocked=%d", accepted, blocked)
	}
	devices, err := s.ListDevices(ctx)
	if err != nil || len(devices) != 1 {
		t.Fatalf("one-TV invariant broken: %v, %+v", err, devices)
	}
	if err := s.RevokeDevice(ctx, devices[0].DeviceID); err != nil {
		t.Fatal(err)
	}
	devices, err = s.ListDevices(ctx)
	if err != nil || len(devices) != 0 {
		t.Fatalf("revoke failed: %v", err)
	}
}

func TestRedisApproveEnforcesSingleDeviceInLua(t *testing.T) {
	reply := -1
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args []json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Error(err)
			return
		}
		if len(args) != 11 {
			t.Errorf("expected Redis EVAL command with 4 keys and 4 arguments; got %d", len(args))
		}
		var command, script string
		_ = json.Unmarshal(args[0], &command)
		_ = json.Unmarshal(args[1], &script)
		if command != "EVAL" || !strings.Contains(script, "redis.call('SCARD', KEYS[3])") ||
			!strings.Contains(script, "return -1") ||
			strings.Index(script, "SCARD") > strings.Index(script, "redis.call('DEL'") {
			t.Errorf("Lua approval must check single-device cardinality before mutating Redis")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"result":%d}`, reply)
	}))
	defer upstream.Close()
	store := RedisStore{Redis: &catalog.RedisStore{
		URL: upstream.URL, Token: "test-only-token", Key: "stremio-catalog-mcp:test", Client: upstream.Client(),
	}}
	p := PairPending{Name: "TV", FCMToken: "valid_token_abcdefghijklmnopqrstuvwxyz", SessionHash: hashSecret("secret")}
	d := Device{ID: "tv_0123456789abcdef0123456789abcdef", Name: "TV"}
	receipt := PairReceipt{Status: "paired", DeviceID: d.ID}
	// The mocked Upstash endpoint is TLS just like production.
	// First response simulates a concurrently paired TV (Lua return -1).
	ok, err := store.Approve(context.Background(), "12345678", p, d, receipt)
	if ok || !errors.Is(err, ErrAlreadyPaired) {
		t.Fatalf("atomic conflict not surfaced: ok=%v err=%v", ok, err)
	}
}
