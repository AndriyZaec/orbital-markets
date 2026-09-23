package dataagent

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
)

func TestStoreEncryptsAndRestoresKeysAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.db")
	key := bytes.Repeat([]byte{0x42}, 32)
	database := openAgentDB(t, path)
	store, err := NewStore(database, key)
	if err != nil {
		t.Fatal(err)
	}
	want := testRecord("probe-one", testOwner, testDataAgent, bytes.Repeat([]byte{0x11}, 32))
	if err := store.SavePending(context.Background(), want, want.ApprovalNonce-1); err != nil {
		t.Fatal(err)
	}
	database.Close()

	database = openAgentDB(t, path)
	t.Cleanup(func() { database.Close() })
	restarted, err := NewStore(database, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Load(context.Background(), want.ProbeID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.PrivateKey, want.PrivateKey) || got.Owner != want.Owner || got.AgentAddress != want.AgentAddress {
		t.Fatalf("restored record = %+v", got)
	}
	var ciphertext []byte
	if err := database.QueryRow(`SELECT key_ciphertext FROM aster_data_agents WHERE probe_id = ?`, want.ProbeID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, want.PrivateKey) {
		t.Fatal("persisted ciphertext contains plaintext key")
	}
}

func TestStoreAuthenticatesAllImmutableMetadata(t *testing.T) {
	tests := []struct {
		name      string
		update    string
		loadProbe string
	}{
		{"probe id", `UPDATE aster_data_agents SET probe_id = 'changed-probe'`, "changed-probe"},
		{"owner", `UPDATE aster_data_agents SET owner_account = '0x3333333333333333333333333333333333333333'`, "probe-tamper"},
		{"execution agent", `UPDATE aster_data_agents SET execution_agent = '0x3333333333333333333333333333333333333333'`, "probe-tamper"},
		{"agent address", `UPDATE aster_data_agents SET agent_address = '0x3333333333333333333333333333333333333333'`, "probe-tamper"},
		{"approval nonce", `UPDATE aster_data_agents SET approval_nonce = approval_nonce + 1`, "probe-tamper"},
		{"requested expiry", `UPDATE aster_data_agents SET requested_expiry = requested_expiry + 1`, "probe-tamper"},
		{"version", `UPDATE aster_data_agents SET key_version = key_version + 1`, "probe-tamper"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openAgentDB(t, filepath.Join(t.TempDir(), "agents.db"))
			defer database.Close()
			store, _ := NewStore(database, bytes.Repeat([]byte{1}, 32))
			record := testRecord("probe-tamper", testOwner, testDataAgent, bytes.Repeat([]byte{3}, 32))
			if err := store.SavePending(context.Background(), record, record.ApprovalNonce-1); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(test.update); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(context.Background(), test.loadProbe); err == nil {
				t.Fatal("tampered authenticated metadata was accepted")
			}
		})
	}
}

func TestStoreRejectsWrongMasterKeyAndCrossOwnerLoad(t *testing.T) {
	database := openAgentDB(t, filepath.Join(t.TempDir(), "agents.db"))
	t.Cleanup(func() { database.Close() })
	store, _ := NewStore(database, bytes.Repeat([]byte{1}, 32))
	record := testRecord("probe-owner", testOwner, testDataAgent, bytes.Repeat([]byte{3}, 32))
	if err := store.SavePending(context.Background(), record, record.ApprovalNonce-1); err != nil {
		t.Fatal(err)
	}
	wrong, _ := NewStore(database, bytes.Repeat([]byte{2}, 32))
	if _, err := wrong.Load(context.Background(), record.ProbeID); err == nil {
		t.Fatal("wrong master key decrypted record")
	}
	if _, err := store.LoadForOwner(context.Background(), record.ProbeID, "0x3333333333333333333333333333333333333333"); err == nil {
		t.Fatal("one owner loaded another owner's key")
	}
}

func TestStoreReplacesOnlyExpiredPendingRecord(t *testing.T) {
	database := openAgentDB(t, filepath.Join(t.TempDir(), "agents.db"))
	t.Cleanup(func() { database.Close() })
	store, _ := NewStore(database, bytes.Repeat([]byte{4}, 32))
	first := testRecord("probe-old", testOwner, testDataAgent, bytes.Repeat([]byte{5}, 32))
	if err := store.SavePending(context.Background(), first, first.ApprovalNonce-1); err != nil {
		t.Fatal(err)
	}
	newer := testRecord("probe-new", testOwner, "0x3333333333333333333333333333333333333333", bytes.Repeat([]byte{6}, 32))
	newer.ApprovalNonce = first.ApprovalNonce + int64(time.Minute/time.Microsecond)
	if err := store.SavePending(context.Background(), newer, first.ApprovalNonce-1); err == nil {
		t.Fatal("fresh pending record was replaced")
	}
	if err := store.SavePending(context.Background(), newer, first.ApprovalNonce+1); err != nil {
		t.Fatalf("expired pending record was not replaced: %v", err)
	}
	got, err := store.Load(context.Background(), newer.ProbeID)
	if err != nil || !bytes.Equal(got.PrivateKey, newer.PrivateKey) {
		t.Fatalf("replacement = %+v, err = %v", got, err)
	}
	for _, status := range []Status{StatusSubmitting, StatusUncertain, StatusRejected, StatusApproved} {
		t.Run("does not replace "+string(status), func(t *testing.T) {
			database := openAgentDB(t, filepath.Join(t.TempDir(), "agents.db"))
			defer database.Close()
			store, _ := NewStore(database, bytes.Repeat([]byte{4}, 32))
			existing := testRecord("probe-existing", testOwner, testDataAgent, bytes.Repeat([]byte{7}, 32))
			if err := store.SavePending(context.Background(), existing, existing.ApprovalNonce-1); err != nil {
				t.Fatal(err)
			}
			if err := store.Transition(context.Background(), existing.ProbeID, StatusPending, status, "test", time.Now()); err != nil {
				t.Fatal(err)
			}
			replacement := testRecord("probe-replacement", testOwner, "0x4444444444444444444444444444444444444444", bytes.Repeat([]byte{8}, 32))
			if err := store.SavePending(context.Background(), replacement, existing.ApprovalNonce+1); err == nil {
				t.Fatalf("%s record was replaced", status)
			}
		})
	}
}

func TestStoreRecoversInterruptedSubmissionAsUncertain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.db")
	key := bytes.Repeat([]byte{0x44}, 32)
	database := openAgentDB(t, path)
	store, err := NewStore(database, key)
	if err != nil {
		t.Fatal(err)
	}
	record := testRecord("probe-interrupted", testOwner, testDataAgent, bytes.Repeat([]byte{0x22}, 32))
	if err := store.SavePending(context.Background(), record, record.ApprovalNonce-1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginSubmission(context.Background(), record.ProbeID); err != nil {
		t.Fatal(err)
	}
	database.Close()

	database = openAgentDB(t, path)
	defer database.Close()
	if _, err := NewStore(database, key); err != nil {
		t.Fatal(err)
	}
	status, err := storeStatus(database, key, record.Owner)
	if err != nil || status.Status != StatusUncertain || status.LastError == "" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func storeStatus(database *sql.DB, key []byte, owner string) (ProbeStatus, error) {
	store, err := NewStore(database, key)
	if err != nil {
		return ProbeStatus{}, err
	}
	return store.StatusByOwner(context.Background(), owner)
}

func TestParseMasterKeyRequiresStrictBase64OfExactly32Bytes(t *testing.T) {
	valid := "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI="
	key, err := ParseMasterKey(valid)
	if err != nil || len(key) != 32 {
		t.Fatalf("valid key: len=%d err=%v", len(key), err)
	}
	for _, invalid := range []string{"", "not-base64", "c2hvcnQ=", valid + "\n"} {
		if _, err := ParseMasterKey(invalid); err == nil {
			t.Fatalf("invalid master key %q was accepted", invalid)
		}
	}
}

func openAgentDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := appdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func testRecord(probeID, owner, agent string, privateKey []byte) Record {
	return Record{
		ProbeID: probeID, Owner: owner, ExecutionAgent: testExecutionAgent,
		AgentAddress: agent, PrivateKey: privateKey, ApprovalNonce: 1_800_000_000_000_000,
		RequestedExpiry: 1_831_536_000_000, Status: StatusPending,
	}
}
