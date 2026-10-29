package tvremote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

const pairTTL = 10 * time.Minute
const commandTTL = 5 * time.Minute

// PairPending is only stored in short-lived private Redis keys.
type PairPending struct {
	Name        string `json:"name"`
	FCMToken    string `json:"fcm_token"`
	SessionHash string `json:"session_hash"`
}

type PairReceipt struct {
	Status       string `json:"status"`
	DeviceID     string `json:"device_id,omitempty"`
	DeviceSecret string `json:"device_secret,omitempty"`
}

type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FCMToken   string `json:"fcm_token"`
	SecretHash string `json:"secret_hash"`
	PairedAt   string `json:"paired_at"`
}

// CommandReceipt is short-lived and contains no FCM token or credentials.
// Pending records are reserved before sending the push so late/spoofed
// acknowledgements cannot create an arbitrary command.
type CommandReceipt struct {
	DeviceID  string `json:"device_id"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Storage is intentionally independent of catalog state and revisions.
type Storage interface {
	RateLimitPair(context.Context, string) (bool, error)
	StartPair(context.Context, string, PairPending) (bool, error)
	PairStatus(context.Context, string) (PairReceipt, error)
	Pending(context.Context, string) (PairPending, error)
	Approve(context.Context, string, PairPending, Device, PairReceipt) (bool, error)
	List(context.Context) ([]Device, error)
	Get(context.Context, string) (Device, error)
	UpdateToken(context.Context, Device, Device) (bool, error)
	Remove(context.Context, string) error
	ReserveCommand(context.Context, string, string) error
	CommandReceipt(context.Context, string) (CommandReceipt, error)
	AcknowledgeCommand(context.Context, string, string, string, string) (bool, error)
	CancelCommand(context.Context, string) error
}

type RedisStore struct {
	Redis *catalog.RedisStore
}

func (s RedisStore) key(suffix string) string { return s.Redis.Key + ":tv:" + suffix }

func (s RedisStore) call(ctx context.Context, args ...any) (json.RawMessage, error) {
	if s.Redis == nil {
		return nil, errors.New("TV Redis store not configured")
	}
	return s.Redis.Do(ctx, args...)
}

func numberResult(result json.RawMessage, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	var number int
	if err := json.Unmarshal(result, &number); err != nil {
		return false, err
	}
	return number == 1, nil
}

func (s RedisStore) RateLimitPair(ctx context.Context, ip string) (bool, error) {
	// Enforce both a global and per-address budget. Keys contain only an IP hash.
	const script = `local n=redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], 600) end
local g=redis.call('INCR', KEYS[2])
if g == 1 then redis.call('EXPIRE', KEYS[2], 600) end
if n > 10 or g > 250 then return 0 end
return 1`
	return numberResult(s.call(ctx, "EVAL", script, 2, s.key("rate:"+ip), s.key("rate:global")))
}

func (s RedisStore) StartPair(ctx context.Context, code string, pending PairPending) (bool, error) {
	encoded, err := json.Marshal(pending)
	if err != nil {
		return false, err
	}
	const script = `if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'EX', 600)
redis.call('SET', KEYS[2], '{"status":"pending"}', 'EX', 600)
return 1`
	return numberResult(s.call(ctx, "EVAL", script, 2, s.key("pair:"+code), s.key("status:"+pending.SessionHash), string(encoded)))
}

func (s RedisStore) load(ctx context.Context, key string, out any) error {
	result, err := s.call(ctx, "GET", key)
	if err != nil {
		return err
	}
	if len(result) == 0 || string(result) == "null" {
		return ErrNotFound
	}
	var encoded string
	if err := json.Unmarshal(result, &encoded); err != nil {
		return err
	}
	return json.Unmarshal([]byte(encoded), out)
}

func (s RedisStore) Pending(ctx context.Context, code string) (PairPending, error) {
	var p PairPending
	err := s.load(ctx, s.key("pair:"+code), &p)
	return p, err
}

func (s RedisStore) PairStatus(ctx context.Context, sessionHash string) (PairReceipt, error) {
	var receipt PairReceipt
	err := s.load(ctx, s.key("status:"+sessionHash), &receipt)
	return receipt, err
}

func (s RedisStore) Approve(ctx context.Context, code string, pending PairPending, device Device, receipt PairReceipt) (bool, error) {
	original, err := json.Marshal(pending)
	if err != nil {
		return false, err
	}
	encodedDevice, err := json.Marshal(device)
	if err != nil {
		return false, err
	}
	encodedReceipt, err := json.Marshal(receipt)
	if err != nil {
		return false, err
	}
	const script = `if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('SCARD', KEYS[3]) ~= 0 then return -1 end
redis.call('DEL', KEYS[1])
redis.call('SET', KEYS[2], ARGV[2])
redis.call('SADD', KEYS[3], ARGV[3])
redis.call('SET', KEYS[4], ARGV[4], 'EX', 600)
return 1`
	result, err := s.call(ctx, "EVAL", script, 4,
		s.key("pair:"+code), s.key("device:"+device.ID), s.key("devices"), s.key("status:"+pending.SessionHash),
		string(original), string(encodedDevice), device.ID, string(encodedReceipt))
	if err != nil {
		return false, err
	}
	var n int
	if err := json.Unmarshal(result, &n); err != nil {
		return false, err
	}
	if n == -1 {
		return false, ErrAlreadyPaired
	}
	return n == 1, nil
}

func (s RedisStore) Get(ctx context.Context, id string) (Device, error) {
	var d Device
	err := s.load(ctx, s.key("device:"+id), &d)
	return d, err
}

func (s RedisStore) List(ctx context.Context) ([]Device, error) {
	result, err := s.call(ctx, "SMEMBERS", s.key("devices"))
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(result, &ids); err != nil {
		return nil, err
	}
	if len(ids) > 32 {
		return nil, fmt.Errorf("unexpected paired-device count: %d", len(ids))
	}
	devices := make([]Device, 0, len(ids))
	for _, id := range ids {
		d, err := s.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (s RedisStore) UpdateToken(ctx context.Context, previous, next Device) (bool, error) {
	oldJSON, err := json.Marshal(previous)
	if err != nil {
		return false, err
	}
	newJSON, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	const script = `if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2])
return 1`
	return numberResult(s.call(ctx, "EVAL", script, 1, s.key("device:"+previous.ID), string(oldJSON), string(newJSON)))
}

func (s RedisStore) Remove(ctx context.Context, id string) error {
	const script = `redis.call('SREM', KEYS[1], ARGV[1])
redis.call('DEL', KEYS[2])
return 1`
	_, err := s.call(ctx, "EVAL", script, 2, s.key("devices"), s.key("device:"+id), id)
	return err
}

func (s RedisStore) ReserveCommand(ctx context.Context, commandID, deviceID string) error {
	value, err := json.Marshal(CommandReceipt{DeviceID: deviceID, Status: "pending"})
	if err != nil {
		return err
	}
	result, err := s.call(ctx, "SET", s.key("command:"+commandID), string(value), "EX", int(commandTTL.Seconds()), "NX")
	if err != nil {
		return err
	}
	var reply string
	if err := json.Unmarshal(result, &reply); err != nil || reply != "OK" {
		return errors.New("unable to register TV command")
	}
	return nil
}

func (s RedisStore) CommandReceipt(ctx context.Context, commandID string) (CommandReceipt, error) {
	var r CommandReceipt
	err := s.load(ctx, s.key("command:"+commandID), &r)
	return r, err
}

func (s RedisStore) AcknowledgeCommand(ctx context.Context, commandID, deviceID, status, when string) (bool, error) {
	// The command must already exist, belong to this TV, and be pending.
	// Keep its original expiration; no arbitrary client can grow the data set.
	const script = `local raw=redis.call('GET',KEYS[1])
if not raw then return 0 end
local record=cjson.decode(raw)
if record.device_id ~= ARGV[1] or record.status ~= 'pending' then return 0 end
record.status=ARGV[2]
record.updated_at=ARGV[3]
redis.call('SET',KEYS[1],cjson.encode(record),'KEEPTTL')
return 1`
	return numberResult(s.call(ctx, "EVAL", script, 1, s.key("command:"+commandID), deviceID, status, when))
}

func (s RedisStore) CancelCommand(ctx context.Context, commandID string) error {
	_, err := s.call(ctx, "DEL", s.key("command:"+commandID))
	return err
}

func hashSecret(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

var ErrNotFound = errors.New("not found or expired")
