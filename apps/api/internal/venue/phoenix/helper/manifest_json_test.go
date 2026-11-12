package helper

import (
	"encoding/json"
	"testing"
)

func TestInstructionManifestJSONPreservesNumericAccountIndexes(t *testing.T) {
	for _, indexes := range []string{"[]", "[0,1,255]"} {
		var instruction InstructionManifest
		if err := json.Unmarshal([]byte(`{"account_indexes":`+indexes+`}`), &instruction); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(instruction)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Indexes json.RawMessage `json:"account_indexes"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		if string(wire.Indexes) != indexes {
			t.Fatalf("account_indexes changed from %s to %s during API serialization", indexes, wire.Indexes)
		}
	}
}

func TestInstructionManifestRejectsOutOfRangeOrNonIntegerIndexes(t *testing.T) {
	for _, indexes := range []string{"[-1]", "[256]", "[1.5]", `["1"]`} {
		var instruction InstructionManifest
		if err := json.Unmarshal([]byte(`{"account_indexes":`+indexes+`}`), &instruction); err == nil {
			t.Fatalf("invalid account indexes accepted: %s", indexes)
		}
	}
}

func TestInstructionManifestReadsPreviouslyStoredByteIndexes(t *testing.T) {
	var instruction InstructionManifest
	if err := json.Unmarshal([]byte(`{"account_indexes":"AAH/"}`), &instruction); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(instruction.AccountIndexes)
	if err != nil || string(encoded) != "[0,1,255]" {
		t.Fatalf("stored manifest indexes were not preserved: %s %v", encoded, err)
	}
}
