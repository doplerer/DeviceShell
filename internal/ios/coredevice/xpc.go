package coredevice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/xpc"
	"github.com/google/uuid"
)

// service is one RemoteXPC connection to a phone service; messages are serialized.
type service struct {
	mu   sync.Mutex
	conn *xpc.Connection
}

func (t *Tunnel) connect(name string) (*service, error) {
	if t.Services[name].Port == 0 {
		return nil, fmt.Errorf("%s not published (is the developer disk image mounted?)", name)
	}
	conn, e := ios.ConnectToXpcServiceTunnelIface(t.Device, name)
	if e != nil {
		return nil, fmt.Errorf("%s: %w", name, e)
	}
	return &service{conn: conn}, nil
}

// send delivers a message that has no reply (HID reports, button events).
func (s *service) send(m map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.Send(m)
}

// request sends a message that wants a reply and waits for it, or for c to end
// (which closes the connection).
func (s *service) request(c context.Context, m map[string]any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.conn.Send(m, xpc.HeartbeatRequestFlag); e != nil {
		return nil, e
	}
	type result struct {
		m map[string]any
		e error
	}
	done := make(chan result, 1)
	go func() {
		reply, e := s.conn.ReceiveOnServerClientStream()
		done <- result{reply, e}
	}()
	select {
	case r := <-done:
		return r.m, r.e
	case <-c.Done():
		s.conn.Close()
		return nil, c.Err()
	}
}

func (s *service) close() { s.conn.Close() }

// coreDeviceRequest is the CoreDevice invocation envelope, as the captures show it.
func coreDeviceRequest(feature, action string, input map[string]any) map[string]any {
	m := map[string]any{
		"CoreDevice.featureIdentifier":            "com.apple.coredevice.feature." + feature,
		"CoreDevice.action":                       map[string]any{},
		"CoreDevice.input":                        input,
		"CoreDevice.invocationIdentifier":         uuid.New().String(),
		"CoreDevice.deviceIdentifier":             uuid.New().String(),
		"CoreDevice.CoreDeviceDDIProtocolVersion": int64(2),
		"CoreDevice.coreDeviceVersion": map[string]any{
			"components":              []any{uint64(629), uint64(3)},
			"originalComponentsCount": int64(2),
			"stringValue":             "629.3",
		},
	}
	if action != "" {
		m["CoreDevice.actionIdentifier"] = "com.apple.coredevice.action." + action
	}
	return m
}

// invoke runs one CoreDevice feature on its own connection and returns its output.
func (t *Tunnel) invoke(c context.Context, name, feature, action string, input map[string]any) (map[string]any, error) {
	s, e := t.connect(name)
	if e != nil {
		return nil, e
	}
	defer s.close()
	reply, e := s.request(c, coreDeviceRequest(feature, action, input))
	if e != nil {
		return nil, fmt.Errorf("%s: %w", feature, e)
	}
	if out, ok := reply["CoreDevice.output"].(map[string]any); ok {
		return out, nil
	}
	return nil, fmt.Errorf("%s: %w", feature, replyError(reply))
}

// replyError is the localized description CoreDevice errors carry, or the raw reply.
func replyError(reply map[string]any) error {
	var find func(v any) string
	find = func(v any) string {
		switch v := v.(type) {
		case map[string]any:
			if s, ok := v["NSLocalizedDescription"].(string); ok {
				return s
			}
			for _, x := range v {
				if s := find(x); s != "" {
					return s
				}
			}
		case []any:
			for _, x := range v {
				if s := find(x); s != "" {
					return s
				}
			}
		}
		return ""
	}
	if s := find(reply); s != "" {
		return errors.New(s)
	}
	text := fmt.Sprint(reply)
	if len(text) > 300 {
		text = text[:300]
	}
	return errors.New(strings.TrimSpace(text))
}

// uuidIn finds a uuid.UUID under the given keys of nested dictionaries.
func uuidIn(v any, path ...string) (uuid.UUID, bool) {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return uuid.UUID{}, false
		}
		v = m[k]
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}
