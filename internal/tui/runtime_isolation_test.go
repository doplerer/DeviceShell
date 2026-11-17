package tui

import (
	"github.com/doplerer/deviceshell/internal/core"
	"testing"
)

func TestInterfacesKeepIndependentCommandRegistries(t *testing.T) {
	first, second := New(core.New()), New(core.New())
	first.Service.Lang = "en"
	first.RebuildTexts()
	second.RebuildTexts()
	if first.lookupCommand("settings").help == second.lookupCommand("settings").help {
		t.Fatal("command language leaked between interfaces")
	}
	first.clipboardWrite = func(string) error { return nil }
	if first.Service == second.Service {
		t.Fatal("interfaces share a service")
	}
}
