package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Store performs optimistic updates so concurrent MCP calls don't lose titles.
type Store interface {
	Load(context.Context) (string, error)
	CAS(context.Context, string, string) (bool, error)
}

type MemoryStore struct {
	mu  sync.Mutex
	raw string
}

func (s *MemoryStore) Load(_ context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw, nil
}

func (s *MemoryStore) CAS(_ context.Context, previous, next string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous != s.raw {
		return false, nil
	}
	s.raw = next
	return true, nil
}

// RedisStore uses Upstash's REST API, requiring no persistent TCP sockets on Vercel.
type RedisStore struct {
	URL    string
	Token  string
	Key    string
	Client *http.Client
}

func (s *RedisStore) command(ctx context.Context, args ...any) (json.RawMessage, error) {
	if !strings.HasPrefix(s.URL, "https://") || s.Token == "" || s.Key == "" {
		return nil, errors.New("durable Redis storage is not configured")
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.URL, "/"), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Redis request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Redis HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Error != "" {
		return nil, fmt.Errorf("Redis command failed: %s", envelope.Error)
	}
	return envelope.Result, nil
}

func (s *RedisStore) Load(ctx context.Context) (string, error) {
	result, err := s.command(ctx, "GET", s.Key)
	if err != nil {
		return "", err
	}
	if string(result) == "null" {
		return "", nil
	}
	var raw string
	if err := json.Unmarshal(result, &raw); err != nil {
		return "", err
	}
	return raw, nil
}

// Redis EVAL makes a whole-document replacement conditional on its prior value.
// This avoids races between agents creating/updating rows in parallel.
func (s *RedisStore) CAS(ctx context.Context, previous, next string) (bool, error) {
	const script = `local old = redis.call('GET', KEYS[1]); if (old or '') ~= ARGV[1] then return 0 end; redis.call('SET', KEYS[1], ARGV[2]); return 1`
	result, err := s.command(ctx, "EVAL", script, 1, s.Key, previous, next)
	if err != nil {
		return false, err
	}
	var ok int
	if err := json.Unmarshal(result, &ok); err != nil {
		return false, err
	}
	return ok == 1, nil
}

// AcquireSyncLock serializes our own Stremio account mutations across Vercel instances.
func (s *RedisStore) AcquireSyncLock(ctx context.Context, token string) (bool, error) {
	// Four sequential Stremio requests can take up to ~40s when upstream is slow.
	// Keep the lease longer than the normal Vercel request lifetime.
	res, err := s.command(ctx, "SET", s.Key+":account-sync", token, "NX", "EX", 60)
	if err != nil {
		return false, err
	}
	return string(res) == `"OK"`, nil
}

func (s *RedisStore) ReleaseSyncLock(ctx context.Context, token string) error {
	const script = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end; return 0`
	_, err := s.command(ctx, "EVAL", script, 1, s.Key+":account-sync", token)
	return err
}
