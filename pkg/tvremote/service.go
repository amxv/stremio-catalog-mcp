package tvremote

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

const maxDevices = 1

var (
	pairCodePattern  = regexp.MustCompile(`^[0-9]{8}$`)
	deviceIDPattern  = regexp.MustCompile(`^tv_[0-9a-f]{32}$`)
	commandIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	tokenPattern     = regexp.MustCompile(`^[A-Za-z0-9_:.-]+$`)
)

func validDeliveryStatus(status string) bool {
	switch status {
	case "launch_requested", "notification_posted", "notification_unavailable":
		return true
	}
	return false
}

type Sender interface {
	Send(context.Context, Device, OpenPayload) (string, error)
}

type Service struct {
	Store    Storage
	Sender   Sender
	Now      func() time.Time
	SetupKey string
	// ReceiptWait overrides the short delivery-receipt wait in tests. Zero uses
	// the production default; a negative value skips waiting.
	ReceiptWait time.Duration
}

func FromEnv(redis *catalog.RedisStore) *Service {
	// Opt in explicitly. Disabled instances expose zero TV tools/endpoints.
	if os.Getenv("TV_OPEN_ENABLED") != "true" {
		return nil
	}
	return &Service{
		Store:    RedisStore{Redis: redis},
		SetupKey: os.Getenv("TV_SETUP_KEY"),
		Sender: &FCMSender{
			ProjectID:          os.Getenv("FCM_PROJECT_ID"),
			ServiceAccountJSON: os.Getenv("FCM_SERVICE_ACCOUNT_JSON"),
		},
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) ready() error {
	if s == nil || s.Store == nil || s.Sender == nil {
		return errors.New("TV integration is disabled or incomplete")
	}
	if fcm, ok := s.Sender.(*FCMSender); ok {
		return fcm.Validate()
	}
	return nil
}

func secret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func deviceID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("tv_%x", b), nil
}

func pairCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%08d", n.Int64()), nil
}

func validFCMToken(token string) bool {
	return len(token) >= 20 && len(token) <= 4096 && tokenPattern.MatchString(token)
}

type PairInput struct {
	Code string `json:"code"`
}
type PairResult struct {
	Paired     bool   `json:"paired"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
}

func (s *Service) StartPair(ctx context.Context, name, token, remoteAddress string) (string, string, error) {
	if err := s.ready(); err != nil {
		return "", "", err
	}
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 60 || strings.ContainsAny(name, "\r\n\x00") {
		return "", "", fmt.Errorf("%w: device name must be 1–60 characters on one line", ErrInvalidInput)
	}
	if !validFCMToken(token) {
		return "", "", fmt.Errorf("%w: invalid FCM registration token", ErrInvalidInput)
	}
	accepted, err := s.Store.RateLimitPair(ctx, hashSecret(remoteAddress))
	if err != nil {
		return "", "", err
	}
	if !accepted {
		return "", "", ErrRateLimited
	}
	session, err := secret()
	if err != nil {
		return "", "", err
	}
	pending := PairPending{Name: name, FCMToken: token, SessionHash: hashSecret(session)}
	for i := 0; i < 5; i++ {
		code, err := pairCode()
		if err != nil {
			return "", "", err
		}
		created, err := s.Store.StartPair(ctx, code, pending)
		if err != nil {
			return "", "", err
		}
		if created {
			return code, session, nil
		}
	}
	return "", "", errors.New("unable to allocate pairing code; retry")
}

func (s *Service) PairStatus(ctx context.Context, session string) (PairReceipt, error) {
	if s == nil || s.Store == nil {
		return PairReceipt{}, errors.New("TV feature disabled")
	}
	if len(session) != 43 || !isURLBase64(session) {
		return PairReceipt{}, ErrNotFound
	}
	return s.Store.PairStatus(ctx, hashSecret(session))
}

func isURLBase64(s string) bool {
	_, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil
}

func (s *Service) ApprovePair(ctx context.Context, input PairInput) (PairResult, error) {
	if err := s.ready(); err != nil {
		return PairResult{}, err
	}
	if !pairCodePattern.MatchString(input.Code) {
		return PairResult{}, fmt.Errorf("%w: pairing code must be eight digits", ErrInvalidInput)
	}
	pending, err := s.Store.Pending(ctx, input.Code)
	if err != nil {
		return PairResult{}, err
	}
	devices, err := s.Store.List(ctx)
	if err != nil {
		return PairResult{}, err
	}
	if len(devices) >= maxDevices {
		return PairResult{}, ErrAlreadyPaired
	}
	id, err := deviceID()
	if err != nil {
		return PairResult{}, err
	}
	key, err := secret()
	if err != nil {
		return PairResult{}, err
	}
	d := Device{ID: id, Name: pending.Name, FCMToken: pending.FCMToken, SecretHash: hashSecret(key), PairedAt: s.now().UTC().Format(time.RFC3339)}
	receipt := PairReceipt{Status: "paired", DeviceID: id, DeviceSecret: key}
	ok, err := s.Store.Approve(ctx, input.Code, pending, d, receipt)
	if err != nil {
		return PairResult{}, err
	}
	if !ok {
		return PairResult{}, errors.New("pairing code already used or expired")
	}
	return PairResult{Paired: true, DeviceID: id, DeviceName: d.Name}, nil
}

type DeviceSummary struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	PairedAt string `json:"paired_at"`
}

// ListDevices is an administrative operation exposed exclusively through
// /tv/admin, never through the MCP tools/list surface.
func (s *Service) ListDevices(ctx context.Context) ([]DeviceSummary, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	list, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]DeviceSummary, 0, len(list))
	for _, d := range list {
		result = append(result, DeviceSummary{DeviceID: d.ID, Name: d.Name, PairedAt: d.PairedAt})
	}
	return result, nil
}

func (s *Service) RevokeDevice(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !deviceIDPattern.MatchString(id) {
		return ErrInvalidInput
	}
	if _, err := s.Store.Get(ctx, id); err != nil {
		return err
	}
	return s.Store.Remove(ctx, id)
}

type OpenInput struct {
	MediaType string `json:"media_type" jsonschema:"movie or series"`
	IMDbID    string `json:"imdb_id" jsonschema:"IMDb tt... ID"`
	Season    *int   `json:"season,omitempty" jsonschema:"Series season, including 0 for specials; requires episode"`
	Episode   *int   `json:"episode,omitempty" jsonschema:"Series episode number (1+); requires season"`
}
type OpenPayload struct {
	MediaType string
	IMDbID    string
	Season    *int
	Episode   *int
	CommandID string
	IssuedAt  int64
}
type OpenResult struct {
	Status         string `json:"status"`
	DeliveryStatus string `json:"delivery_status"`
	CommandID      string `json:"command_id"`
	DeviceName     string `json:"device_name"`
	IMDbID         string `json:"imdb_id"`
	MediaType      string `json:"media_type"`
	Season         *int   `json:"season,omitempty"`
	Episode        *int   `json:"episode,omitempty"`
	Note           string `json:"note"`
}

// DetailLink is intentionally non-autoplay. The empty series video ID opens
// the series detail/episode overview rather than selecting an episode.
func DetailLink(mediaType, imdbID string) (string, error) {
	return EpisodeLink(mediaType, imdbID, nil, nil)
}

// EpisodeLink selects an individual series episode without starting playback.
// Season zero (specials) is valid; episode numbers start at one.
func EpisodeLink(mediaType, imdbID string, season, episode *int) (string, error) {
	if !catalog.ValidType(mediaType) || !catalog.ValidIMDbID(imdbID) {
		return "", errors.New("media_type must be movie or series and imdb_id must be a valid IMDb ID")
	}
	if (season == nil) != (episode == nil) || (season != nil && mediaType != "series") {
		return "", errors.New("season and episode must both be provided, only for a series")
	}
	videoID := imdbID
	if mediaType == "series" {
		videoID = ""
		if season != nil {
			if *season < 0 || *season > 100 || *episode < 1 || *episode > 1000 {
				return "", errors.New("season must be 0–100 and episode 1–1000")
			}
			videoID = fmt.Sprintf("%s:%d:%d", imdbID, *season, *episode)
		}
	}
	return fmt.Sprintf("stremio:///detail/%s/%s/%s", mediaType, imdbID, videoID), nil
}

func (s *Service) OpenTitle(ctx context.Context, input OpenInput) (OpenResult, error) {
	if err := s.ready(); err != nil {
		return OpenResult{}, err
	}
	if _, err := EpisodeLink(input.MediaType, input.IMDbID, input.Season, input.Episode); err != nil {
		return OpenResult{}, err
	}
	list, err := s.Store.List(ctx)
	if err != nil {
		return OpenResult{}, err
	}
	if len(list) == 0 {
		return OpenResult{}, errors.New("no paired Android TV; open /tv/setup to pair the companion app first")
	}
	if len(list) != maxDevices {
		return OpenResult{}, errors.New("multiple legacy TV pairings detected; revoke extra devices at /tv/setup")
	}
	selected := &list[0]
	commandID, err := secret()
	if err != nil {
		return OpenResult{}, err
	}
	if err := s.Store.ReserveCommand(ctx, commandID, selected.ID); err != nil {
		return OpenResult{}, err
	}
	_, err = s.Sender.Send(ctx, *selected, OpenPayload{
		MediaType: input.MediaType, IMDbID: input.IMDbID,
		Season: input.Season, Episode: input.Episode,
		CommandID: commandID, IssuedAt: s.now().Unix(),
	})
	if err != nil {
		_ = s.Store.CancelCommand(ctx, commandID)
		return OpenResult{}, err
	}
	result := OpenResult{
		Status: "accepted_by_fcm", DeliveryStatus: "not_confirmed",
		CommandID: commandID, DeviceName: selected.Name,
		IMDbID: input.IMDbID, MediaType: input.MediaType, Season: input.Season, Episode: input.Episode,
		Note: "Message accepted; TV delivery not confirmed. The TV may be offline.",
	}
	if receipt, err := s.waitForReceipt(ctx, commandID); err == nil && receipt.Status != "pending" {
		result.DeliveryStatus = receipt.Status
		switch receipt.Status {
		case "launch_requested":
			result.Note = "TV received the command and requested Stremio to open. On-screen navigation is not confirmed."
		case "notification_posted":
			result.Note = "TV received the command but posted an Open in Stremio notification instead."
		case "notification_unavailable":
			result.Note = "TV received the command but could not launch Stremio or post a notification."
		}
	}
	return result, nil
}

func (s *Service) waitForReceipt(ctx context.Context, commandID string) (CommandReceipt, error) {
	window := s.ReceiptWait
	if window < 0 {
		return CommandReceipt{Status: "pending"}, nil
	}
	if window == 0 {
		window = 3500 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		receipt, err := s.Store.CommandReceipt(ctx, commandID)
		if err != nil || receipt.Status != "pending" {
			return receipt, err
		}
		select {
		case <-ctx.Done():
			return CommandReceipt{Status: "pending"}, ctx.Err()
		case <-tick.C:
		}
	}
}

// AcknowledgeDelivery accepts only a paired TV's own short-lived command.
// A receipt confirms the receiver attempted a launch, not that Stremio rendered.
func (s *Service) AcknowledgeDelivery(ctx context.Context, deviceID, key, commandID, status string) error {
	if !commandIDPattern.MatchString(commandID) || !validDeliveryStatus(status) {
		return ErrInvalidInput
	}
	if _, err := s.authenticateDevice(ctx, deviceID, key); err != nil {
		return err
	}
	ok, err := s.Store.AcknowledgeCommand(ctx, commandID, deviceID, status, s.now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (s *Service) authenticateDevice(ctx context.Context, id, key string) (Device, error) {
	if s == nil || s.Store == nil || !deviceIDPattern.MatchString(id) || len(key) != 43 || !isURLBase64(key) {
		return Device{}, ErrUnauthorized
	}
	device, err := s.Store.Get(ctx, id)
	if err != nil || subtle.ConstantTimeCompare([]byte(device.SecretHash), []byte(hashSecret(key))) != 1 {
		return Device{}, ErrUnauthorized
	}
	return device, nil
}

func (s *Service) UpdateDeviceToken(ctx context.Context, id, key, token string) error {
	if s == nil || s.Store == nil {
		return errors.New("TV feature disabled")
	}
	if !validFCMToken(token) {
		return ErrInvalidInput
	}
	device, err := s.authenticateDevice(ctx, id, key)
	if err != nil {
		return err
	}
	next := device
	next.FCMToken = token
	updated, err := s.Store.UpdateToken(ctx, device, next)
	if err != nil {
		return err
	}
	if !updated {
		return errors.New("device updated concurrently; retry")
	}
	return nil
}

var (
	ErrUnauthorized  = errors.New("invalid device authentication")
	ErrRateLimited   = errors.New("pairing attempts rate limited")
	ErrInvalidInput  = errors.New("invalid request fields")
	ErrAlreadyPaired = errors.New("a TV is already paired; revoke it at /tv/setup before pairing another")
)
