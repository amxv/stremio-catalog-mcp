package tvremote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

func TestRedisReceiptCommandsAreScopedAndExpiring(t *testing.T) {
	commandID := strings.Repeat("a", 43)
	deviceID := "tv_0123456789abcdef0123456789abcdef"
	var record CommandReceipt
	var present bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-redis-token" {
			t.Error("Redis request lacked authorization")
		}
		var args []any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Error(err)
			return
		}
		command := args[0].(string)
		var reply any
		switch command {
		case "SET":
			if len(args) != 6 || args[1] != "catalog:tv:command:"+commandID || args[3] != "EX" || args[4] != float64(commandTTL.Seconds()) || args[5] != "NX" {
				t.Errorf("command reservation must be private and expire: %v", args)
			}
			if err := json.Unmarshal([]byte(args[2].(string)), &record); err != nil {
				t.Error(err)
			}
			present = true
			reply = "OK"
		case "GET":
			if !present {
				reply = nil
			} else {
				encoded, _ := json.Marshal(record)
				reply = string(encoded)
			}
		case "EVAL":
			if len(args) != 7 || !strings.Contains(args[1].(string), "record.device_id ~= ARGV[1]") ||
				!strings.Contains(args[1].(string), "record.status ~= 'pending'") ||
				!strings.Contains(args[1].(string), "KEEPTTL") || args[3] != "catalog:tv:command:"+commandID {
				t.Errorf("receipt must be bound to the pending command and retain expiry: %v", args[:4])
			}
			if present && record.DeviceID == args[4] && record.Status == "pending" {
				record.Status = args[5].(string)
				record.UpdatedAt = args[6].(string)
				reply = 1
			} else {
				reply = 0
			}
		case "DEL":
			present = false
			reply = 1
		default:
			t.Errorf("unexpected Redis command %q", command)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": reply})
	}))
	defer server.Close()
	store := RedisStore{Redis: &catalog.RedisStore{
		URL: server.URL, Token: "private-redis-token", Key: "catalog", Client: server.Client(),
	}}
	ctx := context.Background()
	if err := store.ReserveCommand(ctx, commandID, deviceID); err != nil {
		t.Fatal(err)
	}
	if record.DeviceID != deviceID || record.Status != "pending" {
		t.Fatalf("invalid reservation: %+v", record)
	}
	ack, err := store.AcknowledgeCommand(ctx, commandID, "tv_ffffffffffffffffffffffffffffffff", "launch_requested", "2026-10-30T00:00:00Z")
	if err != nil || ack {
		t.Fatalf("wrong device acknowledged command: %v, %v", ack, err)
	}
	ack, err = store.AcknowledgeCommand(ctx, commandID, deviceID, "launch_requested", "2026-10-30T00:00:00Z")
	if err != nil || !ack {
		t.Fatalf("valid command acknowledgment failed: %v, %v", ack, err)
	}
	ack, err = store.AcknowledgeCommand(ctx, commandID, deviceID, "notification_posted", "2026-10-30T00:00:01Z")
	if err != nil || ack {
		t.Fatalf("duplicate changed finalized receipt: %v, %v", ack, err)
	}
	got, err := store.CommandReceipt(ctx, commandID)
	if err != nil || got.Status != "launch_requested" || got.UpdatedAt == "" {
		t.Fatalf("receipt not stored: %v %+v", err, got)
	}
	if err := store.CancelCommand(ctx, commandID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommandReceipt(ctx, commandID); err != ErrNotFound {
		t.Fatal(fmt.Errorf("canceled command still exists: %w", err))
	}
}
