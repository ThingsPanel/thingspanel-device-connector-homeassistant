package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	sdk "github.com/thingspanel/device-connector-sdk-go"
)

type fakeLister struct {
	mu    sync.Mutex
	calls int
	reply func(call int) (json.RawMessage, error)
}

func (f *fakeLister) ServiceAccessList(context.Context) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	return f.reply(n)
}

func (f *fakeLister) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeSink struct {
	mu      sync.Mutex
	added   []string
	updated []string
}

func (s *fakeSink) OnDeviceAdd(_ context.Context, r sdk.DeviceAddRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.added = append(s.added, r.DeviceID)
	return nil
}
func (s *fakeSink) OnConfigUpdate(_ context.Context, r sdk.ConfigUpdateRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updated = append(s.updated, r.DeviceID)
	return nil
}

func accessList(devices ...[2]string) json.RawMessage {
	devs := []any{}
	for _, d := range devices {
		devs = append(devs, map[string]any{"id": d[0], "device_number": "ha_" + d[0], "protocol_config": `{"entity_id":"` + d[1] + `"}`, "voucher": `{"username":"u-` + d[0] + `"}`})
	}
	raw, _ := json.Marshal([]any{map[string]any{"voucher": `{"base_url":"http://ha"}`, "devices": devs}})
	return raw
}

func TestSyncOnceReportsOnlyNewAndChangedDevices(t *testing.T) {
	var current json.RawMessage
	lister := &fakeLister{reply: func(int) (json.RawMessage, error) { return current, nil }}
	sink := &fakeSink{}
	s := newDeviceSyncer(lister, sink)

	current = accessList([2]string{"d1", "light.a"})
	if a, u, err := s.SyncOnce(context.Background()); err != nil || a != 1 || u != 0 {
		t.Fatalf("first sync: %d %d %v", a, u, err)
	}
	if a, u, _ := s.SyncOnce(context.Background()); a != 0 || u != 0 {
		t.Fatalf("unchanged list produced work: %d %d", a, u)
	}
	// A device created on the platform later is discovered by the next pull.
	current = accessList([2]string{"d1", "light.a"}, [2]string{"d2", "switch.b"})
	if a, u, _ := s.SyncOnce(context.Background()); a != 1 || u != 0 {
		t.Fatalf("new device not discovered: %d %d", a, u)
	}
	current = accessList([2]string{"d1", "light.changed"}, [2]string{"d2", "switch.b"})
	if a, u, _ := s.SyncOnce(context.Background()); a != 0 || u != 1 {
		t.Fatalf("changed config not reported: %d %d", a, u)
	}
	if len(sink.added) != 2 || sink.added[1] != "d2" || len(sink.updated) != 1 || sink.updated[0] != "d1" {
		t.Fatalf("sink: %+v %+v", sink.added, sink.updated)
	}
	st := s.Status()
	if st.State != "running" || st.Devices != 2 || st.Syncs != 4 || len(st.DeviceNumbers) != 2 || st.LastSyncAt == "" {
		t.Fatalf("status: %+v", st)
	}
}

func TestSyncOnceEmptyAndNullLists(t *testing.T) {
	for _, body := range []string{"", "null", "[]"} {
		lister := &fakeLister{reply: func(int) (json.RawMessage, error) { return json.RawMessage(body), nil }}
		if a, u, err := newDeviceSyncer(lister, &fakeSink{}).SyncOnce(context.Background()); err != nil || a+u != 0 {
			t.Fatalf("%q: %d %d %v", body, a, u, err)
		}
	}
}

func TestRunStopsOnIdentityRejectionWithoutRetrying(t *testing.T) {
	for _, rejected := range []error{sdk.ErrIdentityRequired, sdk.ErrScopeDenied, sdk.ErrNoRuntimeIdentity} {
		lister := &fakeLister{reply: func(int) (json.RawMessage, error) { return nil, rejected }}
		s := newDeviceSyncer(lister, &fakeSink{})
		done := make(chan struct{})
		go func() { s.Run(context.Background(), 5*time.Millisecond); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%v: Run did not stop", rejected)
		}
		if lister.count() != 1 {
			t.Fatalf("%v: retried %d times", rejected, lister.count())
		}
		if st := s.Status(); st.State != "stopped" || st.StopReason == "" {
			t.Fatalf("status: %+v", st)
		}
	}
}

func TestRunKeepsPullingAfterTransientError(t *testing.T) {
	lister := &fakeLister{reply: func(call int) (json.RawMessage, error) {
		if call == 2 {
			return nil, errors.New("temporary 502")
		}
		return accessList([2]string{"d1", "light.a"}), nil
	}}
	s := newDeviceSyncer(lister, &fakeSink{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, 20*time.Millisecond)
	deadline := time.Now().Add(8 * time.Second)
	for lister.count() < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if lister.count() < 4 {
		t.Fatalf("periodic pull stalled after a transient error: %d calls", lister.count())
	}
	if st := s.Status(); st.State == "stopped" {
		t.Fatalf("stopped on transient error: %+v", st)
	}
}

func TestStartupSyncRecoversAfterProlongedBackendOutage(t *testing.T) {
	lister := &fakeLister{reply: func(call int) (json.RawMessage, error) {
		if call <= 40 {
			return nil, errors.New("backend unavailable")
		}
		return accessList([2]string{"d1", "light.a"}), nil
	}}
	s := newDeviceSyncer(lister, &fakeSink{})
	var delays []time.Duration
	s.run(context.Background(), 0, func(_ context.Context, delay time.Duration) bool {
		delays = append(delays, delay)
		return true
	})
	if lister.count() != 41 || s.Status().State != "running" {
		t.Fatalf("sync did not recover: calls=%d status=%+v", lister.count(), s.Status())
	}
	for _, delay := range delays {
		if delay > maxInitialSyncRetryDelay {
			t.Fatalf("retry delay exceeded limit: %s", delay)
		}
	}
}

func TestDeviceSyncIntervalDefaultsAndBounds(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for in, want := range map[string]time.Duration{"": 60 * time.Second, "30": 30 * time.Second, "1": 5 * time.Second, "0": 0, "abc": 60 * time.Second, "-3": 60 * time.Second} {
		if got := deviceSyncInterval(env(in)); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}
