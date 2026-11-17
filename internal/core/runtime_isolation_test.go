package core

import (
	"context"
	"os"
	"testing"
)

func TestServicesKeepIndependentRootsAndLanguages(t *testing.T) {
	first, second := New(), New()
	first.Root, second.Root = t.TempDir(), t.TempDir()
	first.Lang, second.Lang = "en", "es"
	if first.L("hola", "hello") != "hello" || second.L("hola", "hello") != "hola" {
		t.Fatal("language leaked between services")
	}
	for _, service := range []*Service{first, second} {
		if err := service.EnsureLayout(); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.WriteJSON(first.DataPath("independent.json"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(second.DataPath("independent.json")); !os.IsNotExist(err) {
		t.Fatal("storage leaked between roots", err)
	}
	first.ExecuteStep = func(context.Context, Device, Spec) (string, error) { return "first", nil }
	output, err := second.ExecuteStep(context.Background(), Device{}, Spec{Action: "wait", Args: []string{"0"}})
	if err != nil || output == "first" {
		t.Fatal("execution dependency leaked", output, err)
	}
}
