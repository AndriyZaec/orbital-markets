package telegrambot

import "testing"

func TestUpdateDeduperUsesBoundedRecentWindow(t *testing.T) {
	deduper := newUpdateDeduper(2)
	if !deduper.add(1) || deduper.add(1) {
		t.Fatal("duplicate update was not rejected")
	}
	if !deduper.add(2) || !deduper.add(3) {
		t.Fatal("new update was rejected")
	}
	if !deduper.add(1) {
		t.Fatal("evicted update ID was not accepted")
	}
}
