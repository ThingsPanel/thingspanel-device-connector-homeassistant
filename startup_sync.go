package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/thingspanel/device-connector-sdk-go"
)

const (
	defaultDeviceSyncInterval = 60 * time.Second
	minDeviceSyncInterval     = 5 * time.Second
	maxInitialSyncRetryDelay  = 60 * time.Second
)

// accessLister is the part of the SDK platform client the sync needs.
type accessLister interface {
	ServiceAccessList(ctx context.Context) (json.RawMessage, error)
}

// deviceSink receives devices discovered on the platform.
type deviceSink interface {
	OnDeviceAdd(ctx context.Context, req sdk.DeviceAddRequest) error
	OnConfigUpdate(ctx context.Context, req sdk.ConfigUpdateRequest) error
}

// syncStatus is what the plugin reports about its device sync (counts and device numbers only, no credentials).
type syncStatus struct {
	State         string   `json:"state"` // starting | running | stopped
	StopReason    string   `json:"stop_reason,omitempty"`
	LastSyncAt    string   `json:"last_sync_at,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
	Syncs         int      `json:"syncs"`
	Devices       int      `json:"devices"`
	DeviceNumbers []string `json:"device_numbers"`
}

// deviceSyncer remembers which platform devices it already handed to the handler, so a periodic pull only
// reports new or changed devices.
type deviceSyncer struct {
	lister accessLister
	sink   deviceSink
	mu     sync.Mutex
	known  map[string]string // device id -> config fingerprint
	status syncStatus
	now    func() time.Time
}

func newDeviceSyncer(lister accessLister, sink deviceSink) *deviceSyncer {
	return &deviceSyncer{lister: lister, sink: sink, known: map[string]string{}, now: time.Now, status: syncStatus{State: "starting", DeviceNumbers: []string{}}}
}

func (s *deviceSyncer) Status() syncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.status
	out.DeviceNumbers = append([]string{}, s.status.DeviceNumbers...)
	return out
}

func (s *deviceSyncer) stop(reason string) {
	s.mu.Lock()
	s.status.State, s.status.StopReason = "stopped", reason
	s.mu.Unlock()
}

// Run pulls at startup and then every interval. Transient startup errors retry with bounded delay;
// identity or scope rejection stops rather than attempting to revive an invalid identity.
func (s *deviceSyncer) Run(ctx context.Context, interval time.Duration) {
	s.run(ctx, interval, waitForSync)
}

func waitForSync(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *deviceSyncer) run(ctx context.Context, interval time.Duration, waitFor func(context.Context, time.Duration) bool) {
	failures := 0
	first := true
	for {
		wait := interval
		if first {
			wait = min(time.Duration(failures+1)*2*time.Second, maxInitialSyncRetryDelay)
		}
		if !waitFor(ctx, wait) {
			return
		}
		added, updated, err := s.SyncOnce(ctx)
		if err == nil {
			failures, first = 0, false
			if added+updated > 0 {
				slog.Info("device sync: platform devices discovered", "new", added, "updated", updated)
			}
			if interval <= 0 {
				return // sync once only
			}
			continue
		}
		if sdk.IsAuthFailure(err) {
			// A missing, expired, revoked or out-of-scope runtime identity cannot fix itself by retrying.
			slog.Error("device sync stopped: the platform rejected this connector's runtime identity; the installer must issue a new one", "err", err)
			s.stop("runtime identity rejected")
			return
		}
		failures++
		slog.Warn("device sync failed", "failures", failures, "err", err)
	}
}

// SyncOnce pulls service/access/list and reports new devices (OnDeviceAdd) and changed configs (OnConfigUpdate).
func (s *deviceSyncer) SyncOnce(ctx context.Context) (added, updated int, err error) {
	data, err := s.lister.ServiceAccessList(ctx)
	if err != nil {
		s.mu.Lock()
		s.status.LastError = err.Error()
		s.mu.Unlock()
		return 0, 0, err
	}
	var serviceAccesses []map[string]any
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &serviceAccesses); err != nil {
			return 0, 0, fmt.Errorf("decode service accesses: %w", err)
		}
	}

	numbers := []string{}
	seen := map[string]string{}
	for _, sa := range serviceAccesses {
		svcVoucher := parseJSONStringField(sa["voucher"])
		devicesRaw, _ := sa["devices"].([]any)
		for _, devAny := range devicesRaw {
			dev, ok := devAny.(map[string]any)
			if !ok {
				continue
			}
			deviceID, _ := dev["id"].(string)
			if deviceID == "" {
				continue
			}
			deviceNumber, _ := dev["device_number"].(string)

			deviceCfg := make(map[string]any, len(svcVoucher)+4)
			for k, v := range svcVoucher {
				deviceCfg[k] = v
			}
			for k, v := range parseJSONStringField(dev["protocol_config"]) {
				deviceCfg[k] = v
			}
			if deviceNumber != "" {
				deviceCfg["device_number"] = deviceNumber
			}
			devVoucher := parseJSONStringField(dev["voucher"])
			accessToken, _ := devVoucher["username"].(string)

			fingerprint := configFingerprint(deviceCfg, accessToken)
			s.mu.Lock()
			previous, known := s.known[deviceID]
			s.mu.Unlock()
			switch {
			case !known:
				if err := s.sink.OnDeviceAdd(ctx, sdk.DeviceAddRequest{DeviceID: deviceID, DeviceConfig: deviceCfg, AccessToken: accessToken}); err != nil {
					slog.Error("device sync: OnDeviceAdd failed", "deviceID", deviceID, "err", err)
					continue
				}
				added++
			case previous != fingerprint:
				if err := s.sink.OnConfigUpdate(ctx, sdk.ConfigUpdateRequest{DeviceID: deviceID, DeviceConfig: deviceCfg}); err != nil {
					slog.Error("device sync: OnConfigUpdate failed", "deviceID", deviceID, "err", err)
					continue
				}
				updated++
			}
			seen[deviceID] = fingerprint
			if deviceNumber != "" {
				numbers = append(numbers, deviceNumber)
			}
		}
	}
	sort.Strings(numbers)
	s.mu.Lock()
	for id, fp := range seen {
		s.known[id] = fp
	}
	s.status.State, s.status.LastError = "running", ""
	s.status.LastSyncAt = s.now().UTC().Format(time.RFC3339)
	s.status.Syncs++
	s.status.Devices, s.status.DeviceNumbers = len(seen), numbers
	s.mu.Unlock()
	return added, updated, nil
}

func configFingerprint(cfg map[string]any, accessToken string) string {
	raw, _ := json.Marshal(cfg) // map keys are marshalled sorted: stable
	sum := sha256.Sum256(append(raw, []byte("\x00"+accessToken)...))
	return hex.EncodeToString(sum[:])
}

// deviceSyncInterval reads HA_DEVICE_SYNC_INTERVAL_SECONDS (default 60, minimum 5; 0 = startup sync only).
func deviceSyncInterval(getenv func(string) string) time.Duration {
	raw := strings.TrimSpace(getenv("HA_DEVICE_SYNC_INTERVAL_SECONDS"))
	if raw == "" {
		return defaultDeviceSyncInterval
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		slog.Warn("invalid HA_DEVICE_SYNC_INTERVAL_SECONDS, using default", "value", raw)
		return defaultDeviceSyncInterval
	}
	if seconds == 0 {
		return 0
	}
	if d := time.Duration(seconds) * time.Second; d >= minDeviceSyncInterval {
		return d
	}
	return minDeviceSyncInterval
}

// startStartupSync starts the device sync (startup pull plus periodic pull). Service-access devices have no
// device_config_id, so the backend heartbeat bootstrap sync does not push them to the connector.
func startStartupSync(ctx context.Context, info sdk.ConnectorInfo, platform *sdk.PlatformClient, handler *homeAssistantServiceHandler) *deviceSyncer {
	if strings.TrimSpace(info.BackendURL) == "" {
		slog.Warn("device sync disabled: THINGSPANEL_BACKEND_URL not set")
		return nil
	}
	syncer := newDeviceSyncer(platform, handler)
	go syncer.Run(ctx, deviceSyncInterval(os.Getenv))
	return syncer
}

// serveSyncStatus exposes the sync status as JSON on addr (default :9002, HA_STATUS_ADDR; "off" disables).
func serveSyncStatus(ctx context.Context, syncer *deviceSyncer, addr string) {
	if syncer == nil || addr == "off" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sync-status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(syncer.Status())
	})
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("sync status listener failed", "addr", addr, "err", err)
		}
	}()
}

func parseJSONStringField(v any) map[string]any {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return map[string]any{}
	}
	return result
}
