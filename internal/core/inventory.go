package core

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

// aliasPrefix starts the alias of every new device: DEV-001, DEV-002…
const aliasPrefix = "DEV-"

func (app *Service) LoadInventory(path string) ([]Device, error) {
	var records []Device
	e := app.ReadJSON(path, &records)
	if os.IsNotExist(e) {
		return []Device{}, nil
	}
	if e != nil {
		return nil, e
	}
	if records == nil {
		return nil, errors.New(app.L("Inventario invalido: se esperaba una lista JSON, no null", "Invalid inventory: expected a JSON list, not null"))
	}
	return records, nil
}
func (app *Service) MergeInventory(old, live []Device, now string) ([]Device, error) {
	records := map[string]Device{}
	next := 1
	for _, d := range old {
		if d.Serial == "" {
			return nil, errors.New(app.L("Inventario invalido: serie vacia", "Invalid inventory: empty serial"))
		}
		if _, ok := records[d.Serial]; ok {
			return nil, errors.New(app.L("Inventario invalido: serie repetida", "Invalid inventory: repeated serial"))
		}
		d.State, d.Transport = "disconnected", ""
		records[d.Serial] = d
		var n int
		if _, e := fmt.Sscanf(d.Alias, aliasPrefix+"%d", &n); e == nil && n >= next {
			next = n + 1
		}
	}
	ordered := append([]Device(nil), live...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Serial < ordered[j].Serial
	})
	for _, d := range ordered {
		if d.Serial == "" {
			return nil, errors.New(app.L("Serie vacia en la deteccion", "Empty serial while detecting devices"))
		}
		v, ok := records[d.Serial]
		if !ok && iosAwaitingActivation(d) {
			// A new iPhone gets no alias until it is ready (PendingIOS counts it).
			continue
		}
		if !ok {
			v = Device{Serial: d.Serial, Alias: fmt.Sprintf(aliasPrefix+"%03d", next), FirstSeen: now}
			next++
		}
		v.Platform = d.OS()
		v.OSVersion = d.OSVersion
		v.State = d.State
		v.Transport = d.Transport
		v.LastSeen = now
		if d.Model != "" {
			v.Model = d.Model
		}
		records[d.Serial] = v
	}
	out := make([]Device, 0, len(records))
	for _, d := range records {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Alias == out[j].Alias {
			return out[i].Serial < out[j].Serial
		}
		return out[i].Alias < out[j].Alias
	})
	return out, nil
}
