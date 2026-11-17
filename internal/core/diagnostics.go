package core

import (
	"sync"
	"time"
)

type DiagnosticTail struct {
	mu   sync.Mutex
	data []byte
}

func (b *DiagnosticTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(p) > 65536 {
		p = p[len(p)-65536:]
	}
	b.data = append(b.data, p...)
	if len(b.data) > 65536 {
		b.data = append([]byte(nil), b.data[len(b.data)-65536:]...)
	}
	return n, nil
}
func (b *DiagnosticTail) Close() error {
	return nil
}
func (b *DiagnosticTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
func (app *Service) NewDiagnosticTail() (*DiagnosticTail, error) {
	return &DiagnosticTail{}, nil
}

type DiagnosticSummary struct {
	Version int      `json:"version"`
	ID      string   `json:"id"`
	Started string   `json:"started"`
	Devices []string `json:"devices"`
	Events  uint64   `json:"events,omitempty"`
	Failed  bool     `json:"failed"`
}

func (app *Service) SaveDiagnostic(kind, id string, devices []string, events uint64, failed bool) error {
	return app.WithStore(func() error {
		p := app.LogsPath(kind, id+".json")
		if e := app.WriteJSON(p, DiagnosticSummary{2, id, time.Now().Format(time.RFC3339), devices, events, failed}); e != nil {
			return e
		}
		return app.PruneReports(app.LogsPath(kind), time.Now())
	})
}
