package stream

import (
	"testing"
	"time"
)

func TestPatientCacheBasic(t *testing.T) {
	pc := NewPatientCache(100 * time.Millisecond)

	// Initially invalid
	_, _, _, _, _, valid := pc.Get()
	if valid {
		t.Fatal("expected cache to be invalid initially")
	}

	// Store data
	pc.Store("Ahmet", "12345678901", "Nystagmus history", "/path/to/Ahmet_20260630")

	// Get data
	name, patientID, history, dir, count, valid := pc.Get()
	if !valid {
		t.Fatal("expected cache to be valid")
	}
	if name != "Ahmet" || patientID != "12345678901" || history != "Nystagmus history" || dir != "/path/to/Ahmet_20260630" || count != 1 {
		t.Fatalf("unexpected cache values: %s, %s, %s, %s, %d", name, patientID, history, dir, count)
	}

	// Increment record count
	newCount := pc.IncrementRecordCount()
	if newCount != 2 {
		t.Fatalf("expected count 2, got %d", newCount)
	}

	_, _, _, _, countAfterInc, _ := pc.Get()
	if countAfterInc != 2 {
		t.Fatalf("expected count in cache to be 2, got %d", countAfterInc)
	}

	// Invalidate
	pc.Invalidate()
	_, _, _, _, _, valid = pc.Get()
	if valid {
		t.Fatal("expected cache to be invalid after invalidate")
	}
}

func TestPatientCacheExpiry(t *testing.T) {
	pc := NewPatientCache(50 * time.Millisecond)
	pc.Store("Mehmet", "", "", "/path/to/Mehmet")

	// Valid immediately
	_, _, _, _, _, valid := pc.Get()
	if !valid {
		t.Fatal("expected cache to be valid")
	}

	// Wait for expiry
	time.Sleep(60 * time.Millisecond)

	_, _, _, _, _, valid = pc.Get()
	if valid {
		t.Fatal("expected cache to be expired")
	}
}
