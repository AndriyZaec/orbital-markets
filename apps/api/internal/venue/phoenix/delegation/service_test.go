package delegation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

func key(n byte) ed25519.PrivateKey { return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{n}, 32)) }
func address(n byte) string         { return base58.Encode(key(n)[32:]) }

type backend struct {
	mu                        sync.Mutex
	current, target, final    string
	apply, ambiguous, notSent bool
	height                    uint64
	currentSlot               uint64
	chainStatus               *rpc.SignatureStatus
	readErr                   error
	sends                     atomic.Int32
	beforeSend                func()
	signer                    *relayer.Signer
}

func (b *backend) Address() string { return b.signer.Address() }
func (b *backend) LatestBlockhash(context.Context) (rpc.Blockhash, error) {
	return rpc.Blockhash{Hash: base58.Encode(bytes.Repeat([]byte{9}, 32)), LastValidBlockHeight: 150, Slot: 100}, nil
}
func (b *backend) BuildDelegation(_ context.Context, q helper.BuildDelegationRequest) (helper.BuildDelegationResult, error) {
	b.mu.Lock()
	b.target = q.NewPositionAuthority
	b.mu.Unlock()
	message := []byte{2, 1, 1, 3}
	for _, a := range []string{q.FeePayer, q.OwnerAuthority, helper.PhoenixProgramID} {
		p, _ := base58.Decode(a)
		message = append(message, p...)
	}
	hash, _ := base58.Decode(q.RecentBlockhash)
	message = append(message, hash...)
	message = append(message, 1, 2, 1, 1, 1, 42)
	digest := sha256.Sum256(message)
	return helper.BuildDelegationResult{MessageBase64: base64.StdEncoding.EncodeToString(message), MessageSHA256: hex.EncodeToString(digest[:]), RecentBlockhash: q.RecentBlockhash, LastValidBlockHeight: q.LastValidBlockHeight, TraderAccount: address(5), RequiredSigners: []helper.RequiredSigner{{Index: 0, Pubkey: q.FeePayer, Role: "fee_payer"}, {Index: 1, Pubkey: q.OwnerAuthority, Role: "owner_authority"}}}, nil
}
func (b *backend) ReadAuthority(_ context.Context, _, _ string, slot uint64, final bool) (Authority, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.readErr != nil {
		return Authority{}, b.readErr
	}
	p := b.current
	if final && b.final != "" {
		p = b.final
	}
	observed := uint64(110)
	if !final {
		observed = max(observed, b.currentSlot)
	}
	return Authority{PositionAuthority: p, Slot: max(observed, slot)}, nil
}
func (b *backend) CoSignDelegation(_ context.Context, built helper.BuildDelegationResult, owner, sig string, _ uint64) ([]byte, error) {
	return b.signer.CoSignDelegation(built, owner, sig)
}
func (b *backend) SimulateAndSend(_ context.Context, packet []byte, _, _ uint64) (string, error) {
	b.sends.Add(1)
	if b.beforeSend != nil {
		b.beforeSend()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.notSent {
		return "", errors.New("simulation failed")
	}
	if b.apply {
		b.current = b.target
	}
	sig := base58.Encode(packet[1:65])
	if b.ambiguous {
		return sig, &rpc.AmbiguousSendError{Signature: sig, Cause: context.DeadlineExceeded}
	}
	return sig, nil
}
func (b *backend) SignatureStatus(context.Context, string) (*rpc.SignatureStatus, error) {
	return b.chainStatus, nil
}
func (b *backend) FinalizedBlockhashValidity(context.Context, string, uint64) (rpc.BlockhashValidity, error) {
	return rpc.BlockhashValidity{Valid: b.height <= 150, Slot: 200}, nil
}

func setup(t *testing.T) (*Service, *backend, *sql.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "delegation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	store, err := NewStore(database, database)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := relayer.NewSigner(base58.Encode(key(3)))
	if err != nil {
		t.Fatal(err)
	}
	b := &backend{current: address(1), height: 100, signer: signer}
	return NewService(store, b), b, database
}
func prepare(t *testing.T, s *Service, action helper.DelegationAction) Result {
	t.Helper()
	r, err := s.Prepare(context.Background(), PrepareRequest{Owner: address(1), Agent: address(2), Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func signed(t *testing.T, r Result) domain.SignedAction {
	t.Helper()
	var payload struct {
		MessageBase64 string `json:"message_base64"`
	}
	if err := json.Unmarshal(r.SigningRequest.UnsignedPayload, &payload); err != nil {
		t.Fatal(err)
	}
	m, err := base64.StdEncoding.DecodeString(payload.MessageBase64)
	if err != nil {
		t.Fatal(err)
	}
	return domain.SignedAction{RequestID: r.RequestID, ClientOrderID: r.RequestID, Venue: "phoenix", SignerAddress: r.Owner, Signature: base58.Encode(ed25519.Sign(key(1), m))}
}
func authorization(t *testing.T, database *sql.DB) string {
	t.Helper()
	var agent string
	err := database.QueryRow(`SELECT agent_address FROM agent_authorizations WHERE venue='phoenix' AND owner_account=?`, address(1)).Scan(&agent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return agent
}

func TestAcceptanceWaitsForAuthorityAndSurvivesRestart(t *testing.T) {
	s, b, database := setup(t)
	r := prepare(t, s, helper.ActionDelegate)
	b.beforeSend = func() {
		saved, err := s.store.load(context.Background(), r.Owner, r.RequestID)
		if err != nil || saved.State != "pending" || saved.Signature == "" {
			t.Error("send happened before durable pending signature")
		}
	}
	b.ambiguous = true
	result, err := s.Submit(context.Background(), r.Owner, signed(t, r))
	if err != nil || result.State != "pending" || result.Signature == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if authorization(t, database) != "" {
		t.Fatal("RPC acceptance registered an agent")
	}
	store, _ := NewStore(database, database)
	s = NewService(store, b)
	result, err = s.Submit(context.Background(), r.Owner, signed(t, r))
	if err != nil || result.State != "pending" || b.sends.Load() != 1 {
		t.Fatal("restart/repeated submit sent twice")
	}
	result, err = s.Reconcile(context.Background(), r.Owner, r.RequestID)
	if err != nil || result.State != "pending" {
		t.Fatalf("unconfirmed=%+v %v", result, err)
	}
	b.current = address(2)
	result, err = s.Reconcile(context.Background(), r.Owner, r.RequestID)
	if err != nil || result.State != "confirmed" || authorization(t, database) != address(2) {
		t.Fatalf("confirmation=%+v err=%v", result, err)
	}
	b.current = address(4)
	result, err = s.Reconcile(context.Background(), r.Owner, r.RequestID)
	if err != nil || result.State != "superseded" || authorization(t, database) != "" {
		t.Fatalf("external replacement=%+v %v", result, err)
	}
}

func TestRevokeOnlyClearsAuthorizationAfterOwnerReset(t *testing.T) {
	s, b, database := setup(t)
	b.current = address(2)
	_, err := database.Exec(`INSERT INTO agent_authorizations VALUES('phoenix',?,?,?)`, address(1), address(2), time.Now().String())
	if err != nil {
		t.Fatal(err)
	}
	r := prepare(t, s, helper.ActionRevoke)
	if _, err := s.Submit(context.Background(), r.Owner, signed(t, r)); err != nil {
		t.Fatal(err)
	}
	if authorization(t, database) != address(2) {
		t.Fatal("revoke acceptance deleted authorization")
	}
	b.current = address(1)
	result, err := s.Reconcile(context.Background(), r.Owner, r.RequestID)
	if err != nil || result.State != "confirmed" || authorization(t, database) != "" {
		t.Fatalf("revoke=%+v %v", result, err)
	}
}

func TestReplacementAndAuthorityChangeGuards(t *testing.T) {
	s, b, _ := setup(t)
	b.current = address(4)
	q := PrepareRequest{Owner: address(1), Agent: address(2), Action: helper.ActionDelegate}
	result, err := s.Prepare(context.Background(), q)
	if !errors.Is(err, ErrReplacementRequired) || result.CurrentAuthority != address(4) {
		t.Fatalf("replacement guard=%+v %v", result, err)
	}
	q.ReplaceExistingDelegate = true
	q.ExpectedPositionAuthority = address(5)
	if _, err := s.Prepare(context.Background(), q); !errors.Is(err, ErrReplacementRequired) {
		t.Fatalf("stale replacement confirmation accepted: %v", err)
	}
	q.ExpectedPositionAuthority = address(4)
	r, err := s.Prepare(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	b.current = address(5)
	result, err = s.Submit(context.Background(), r.Owner, signed(t, r))
	if !errors.Is(err, ErrAuthorityChanged) || b.sends.Load() != 0 || result.State != "failed" {
		t.Fatalf("changed authority=%+v %v", result, err)
	}
	q.Action = helper.ActionRevoke
	if _, err = s.Prepare(context.Background(), q); !errors.Is(err, ErrAuthorityChanged) {
		t.Fatalf("stale agent revoked replacement: %v", err)
	}
}

func TestOwnerProofAndOneShotConcurrency(t *testing.T) {
	s, b, _ := setup(t)
	r := prepare(t, s, helper.ActionDelegate)
	bad := signed(t, r)
	bad.Signature = base58.Encode(ed25519.Sign(key(2), []byte("wrong")))
	if _, err := s.Submit(context.Background(), r.Owner, bad); !errors.Is(err, relayer.ErrInvalidSignature) {
		t.Fatalf("agent self-approved: %v", err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	b.beforeSend = func() { close(started); <-release }
	done := make(chan error, 1)
	action := signed(t, r)
	go func() { _, err := s.Submit(context.Background(), r.Owner, action); done <- err }()
	<-started
	result, err := s.Submit(context.Background(), r.Owner, action)
	close(release)
	if err != nil || result.State != "pending" {
		t.Fatalf("concurrent duplicate=%+v %v", result, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b.sends.Load() != 1 {
		t.Fatal("duplicate send")
	}
}

func TestUnknownExpiryNeedsFinalizedProofAndNoHistoricalActivation(t *testing.T) {
	for _, historicalMatch := range []bool{false, true} {
		s, b, database := setup(t)
		r := prepare(t, s, helper.ActionDelegate)
		if _, err := s.Submit(context.Background(), r.Owner, signed(t, r)); err != nil {
			t.Fatal(err)
		}
		b.height = 151
		if historicalMatch {
			b.final = address(2)
			b.current = address(4)
			b.currentSlot = 300
		}
		result, err := s.Reconcile(context.Background(), r.Owner, r.RequestID)
		want := "expired"
		if historicalMatch {
			want = "superseded"
		}
		if err != nil || result.State != want || authorization(t, database) != "" {
			t.Fatalf("expiry=%+v %v", result, err)
		}
	}
}

func TestConfirmationAndAuthorizationAreAtomic(t *testing.T) {
	s, b, database := setup(t)
	b.apply = true
	r := prepare(t, s, helper.ActionDelegate)
	if _, err := s.Submit(context.Background(), r.Owner, signed(t, r)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_authorization BEFORE INSERT ON agent_authorizations BEGIN SELECT RAISE(FAIL,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile(context.Background(), r.Owner, r.RequestID); err == nil {
		t.Fatal("authorization failure reported confirmation")
	}
	saved, err := s.store.load(context.Background(), r.Owner, r.RequestID)
	if err != nil || saved.State != "pending" {
		t.Fatalf("confirmation did not roll back: %+v %v", saved, err)
	}
	if _, err := database.Exec(`DROP TRIGGER fail_authorization`); err != nil {
		t.Fatal(err)
	}
	result, err := s.Reconcile(context.Background(), r.Owner, r.RequestID)
	if err != nil || result.State != "confirmed" {
		t.Fatalf("retry reconciliation=%+v %v", result, err)
	}
}

func TestFailedSubmissionAndRPCOutageNeverAuthorize(t *testing.T) {
	s, b, database := setup(t)
	r := prepare(t, s, helper.ActionDelegate)
	b.notSent = true
	result, err := s.Submit(context.Background(), r.Owner, signed(t, r))
	if err != nil || result.State != "failed" || authorization(t, database) != "" {
		t.Fatalf("not sent=%+v %v", result, err)
	}
	b.notSent = false
	r = prepare(t, s, helper.ActionDelegate)
	if _, err := s.Submit(context.Background(), r.Owner, signed(t, r)); err != nil {
		t.Fatal(err)
	}
	b.readErr = errors.New("account unavailable")
	if _, err := s.Reconcile(context.Background(), r.Owner, r.RequestID); err == nil {
		t.Fatal("RPC outage treated as verified")
	}
	b.readErr = nil
	b.chainStatus = &rpc.SignatureStatus{Slot: 120, ConfirmationStatus: "confirmed", Err: json.RawMessage(`"InstructionError"`)}
	result, err = s.Reconcile(context.Background(), r.Owner, r.RequestID)
	if err != nil || result.State != "failed" || authorization(t, database) != "" {
		t.Fatalf("chain failure=%+v %v", result, err)
	}
}

func TestFinalizedNewerAuthoritySnapshotConfirmsWithoutStaleActivation(t *testing.T) {
	s, b, database := setup(t)
	r := prepare(t, s, helper.ActionDelegate)
	if _, err := s.Submit(context.Background(), r.Owner, signed(t, r)); err != nil {
		t.Fatal(err)
	}
	b.height = 151
	b.final = address(2)
	result, err := s.Reconcile(context.Background(), r.Owner, r.RequestID)
	if err != nil || result.State != "confirmed" || authorization(t, database) != address(2) {
		t.Fatalf("newer finalized state=%+v %v", result, err)
	}
}

func TestPreparedExpiryAndOwnerRequestBinding(t *testing.T) {
	s, b, database := setup(t)
	r := prepare(t, s, helper.ActionDelegate)
	again := prepare(t, s, helper.ActionDelegate)
	if again.RequestID != r.RequestID {
		t.Fatal("repeated prepare created extra signable message")
	}
	if _, err := s.Submit(context.Background(), address(4), signed(t, r)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("request moved between owners: %v", err)
	}
	stored, err := s.store.load(context.Background(), r.Owner, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Request.ExpiresAt = time.Now().Add(-time.Second)
	raw, _ := json.Marshal(stored)
	if _, err := database.Exec(`UPDATE phoenix_delegations SET record_json=?,expires_unix_ns=? WHERE request_id=?`, string(raw), stored.Request.ExpiresAt.UnixNano(), r.RequestID); err != nil {
		t.Fatal(err)
	}
	result, err := s.Submit(context.Background(), r.Owner, signed(t, r))
	if err != nil || result.State != "expired" || b.sends.Load() != 0 {
		t.Fatalf("expired submit=%+v %v", result, err)
	}
	fresh := prepare(t, s, helper.ActionDelegate)
	if fresh.RequestID == r.RequestID {
		t.Fatal("expired request reused")
	}
	if _, err := s.Submit(context.Background(), r.Owner, signed(t, r)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old request remained signable: %v", err)
	}
}

func TestAuthorityStatusRestoresWithoutAnOldRequestAndNeverRegisters(t *testing.T) {
	s, b, database := setup(t)
	b.current = address(2)
	status, err := s.Status(context.Background(), address(1), address(2))
	if err != nil || status.Authorized || status.PositionAuthority != address(2) {
		t.Fatalf("unregistered status=%+v %v", status, err)
	}
	if authorization(t, database) != "" {
		t.Fatal("read-only status registered an agent")
	}
	if _, err := database.Exec(`INSERT INTO agent_authorizations VALUES('phoenix',?,?,?)`, address(1), address(2), time.Now().String()); err != nil {
		t.Fatal(err)
	}
	status, err = s.Status(context.Background(), address(1), address(2))
	if err != nil || !status.Authorized {
		t.Fatalf("registered status=%+v %v", status, err)
	}
	b.current = address(4)
	status, err = s.Status(context.Background(), address(1), address(2))
	if err != nil || status.Authorized {
		t.Fatalf("superseded status=%+v %v", status, err)
	}
}
