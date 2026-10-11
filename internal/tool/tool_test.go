package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func noopHandler(context.Context, Env, json.RawMessage) (string, error) { return "", nil }

func TestRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(Def{Name: "a", Description: "first", Handler: noopHandler})

	got, ok := r.Get("a")
	if !ok {
		t.Fatal("Get(a) not found")
	}
	if got.Description != "first" {
		t.Errorf("Description = %q, want %q", got.Description, "first")
	}
	if _, ok := r.Get("missing"); ok {
		t.Error("Get(missing) unexpectedly found")
	}
}

func TestAllPreservesRegistrationOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(Def{Name: "a", Handler: noopHandler})
	r.Register(Def{Name: "b", Handler: noopHandler})
	r.Register(Def{Name: "c", Handler: noopHandler})

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() len = %d, want 3", len(all))
	}
	for i, want := range []string{"a", "b", "c"} {
		if all[i].Name != want {
			t.Errorf("All()[%d].Name = %q, want %q", i, all[i].Name, want)
		}
	}
}

func TestRegisterReplacesInPlace(t *testing.T) {
	r := NewRegistry()
	r.Register(Def{Name: "a", Description: "old", Handler: noopHandler})
	r.Register(Def{Name: "b", Handler: noopHandler})
	r.Register(Def{Name: "a", Description: "new", Handler: noopHandler})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
	if all[0].Name != "a" || all[0].Description != "new" {
		t.Errorf("All()[0] = %+v, want replacement a/new", all[0])
	}
	if all[1].Name != "b" {
		t.Errorf("All()[1].Name = %q, want b", all[1].Name)
	}
}

func TestRegisterIgnoresIncompleteDefs(t *testing.T) {
	r := NewRegistry()
	r.Register(Def{Name: "", Handler: noopHandler})
	r.Register(Def{Name: "no-handler"})

	if all := r.All(); len(all) != 0 {
		t.Fatalf("All() len = %d, want 0", len(all))
	}
}

func TestNilRegistryIsUsable(t *testing.T) {
	var r *Registry
	r.Register(Def{Name: "a", Handler: noopHandler})
	if all := r.All(); all != nil {
		t.Errorf("All() = %v, want nil", all)
	}
	if _, ok := r.Get("a"); ok {
		t.Error("Get(a) on nil registry returned ok")
	}
}

func TestRegisterOverwritesDescriptionAtomically(t *testing.T) {
	r := NewRegistry()
	r.Register(Def{Name: "a", Handler: noopHandler})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			r.Register(Def{Name: "a", Description: "x", Handler: noopHandler})
			_ = r.All()
		}
	}()
	for i := 0; i < 100; i++ {
		_ = r.All()
	}
	<-done
}
