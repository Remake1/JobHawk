package subscribers

import "testing"

func TestStore(t *testing.T) {
	store := NewStore()
	store.Add(42)
	store.Add(42)

	if !store.Contains(42) || len(store.All()) != 1 {
		t.Fatal("subscriber was not stored exactly once")
	}

	store.Remove(42)
	if store.Contains(42) {
		t.Fatal("subscriber was not removed")
	}
}
