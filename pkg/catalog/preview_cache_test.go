package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRedisPreviewCacheUsesSeparateExpiringKey(t *testing.T) {
	const rowID = "row_movie"
	stateKey := "stremio-catalog-mcp:v1:catalogs"
	previewKey := stateKey + ":preview:" + rowID
	cacheValue := `{"tt0371746":{"id":"tt0371746","runtime":"126 min"}}`
	var commands [][]any
	var value string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args []any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Errorf("invalid Redis request: %v", err)
		}
		commands = append(commands, args)
		var result any
		switch args[0] {
		case "SET":
			if args[1] != previewKey || args[3] != "EX" || args[4] != float64(86400) {
				t.Errorf("cache should use distinct 24h key: %+v", args)
			}
			value = args[2].(string)
			result = "OK"
		case "GET":
			if args[1] != previewKey {
				t.Errorf("lookup used wrong key: %+v", args)
			}
			result = value
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	defer srv.Close()
	store := &RedisStore{URL: srv.URL, Token: "test-token", Key: stateKey, Client: srv.Client()}
	if err := store.SavePreviewCache(context.Background(), rowID, cacheValue, 86400); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadPreviewCache(context.Background(), rowID)
	if err != nil || loaded != cacheValue {
		t.Fatalf("saved preview was not loaded: %q (%v)", loaded, err)
	}
	if len(commands) != 2 || !reflect.DeepEqual(commands[0][:2], []any{"SET", previewKey}) {
		t.Fatalf("wrong Redis cache commands: %+v", commands)
	}
	if err := store.SavePreviewCache(context.Background(), "bad-id", cacheValue, 86400); err == nil {
		t.Fatal("invalid row ID accepted")
	}
}
