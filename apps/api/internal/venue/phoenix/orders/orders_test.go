package orders

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	phoenixlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

// Same exact Rise IX 0.6.13 native packet as the browser signing fixture.
const nativeFixture = "AgECC+1JKMYo0cLG6ukDOJBZlWEpWSc6XGP5NjbBRhSshzfRgTl3Dqh9F19Wo1Rmw0x+zMuNipG07jeiXfYPW4/Js5QKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLCwsLDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwNDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4ODg4OF7YA7XQ3QldK1JBMzFbj6qwjDHIAa34acKlwgjHpTeUdry9cM9Jsm1X/mPk3etwvxhICn8kjDVklqOGkWk5Hys5w1vtWjkPr/JZKkBVsO+lWOwgXBnyraU68g+/ds36G6FcmwLK1vPr2Zot8G4Np+wHJmF/MFn8LQpm/HX0ewqUJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQEJCgkKCAEHAgUGAwRJWnbA/MBjJ5ECAAEQJwAAAAAAAB0AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAP//////////AAAAAAAAAAABpAAAAAAAAACAAA=="

func testKey(seed byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, 32))
}
func testAddress(seed byte) string { return base58.Encode(testKey(seed).Public().(ed25519.PublicKey)) }

func requestFixture(t *testing.T) (*domain.SigningRequest, domain.SignedAction) {
	t.Helper()
	message, err := base64.StdEncoding.DecodeString(nativeFixture)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(message)
	trader := base58.Encode(message[4+7*32 : 4+8*32])
	blockhash := base58.Encode(bytes.Repeat([]byte{9}, 32))
	const cid = "18446744073709551615"
	built := helper.BuildOrderResult{MessageBase64: nativeFixture, MessageSHA256: hex.EncodeToString(hash[:]), RecentBlockhash: blockhash, LastValidBlockHeight: 150, MetadataGeneration: 7, TraderAccount: trader,
		RequiredSigners: []helper.RequiredSigner{{Index: 0, Pubkey: testAddress(3), Role: "fee_payer"}, {Index: 1, Pubkey: testAddress(2), Role: "position_authority"}},
		Manifest: helper.OrderManifest{Format: "solana_legacy_message", PhoenixProgramID: helper.PhoenixProgramID, Routing: "native", IntentAction: helper.ActionUnwind, OwnerAuthority: testAddress(1), PositionAuthority: testAddress(2), FeePayer: testAddress(3), TraderAccount: trader,
			RequestedSymbol: "SOL", Orderbook: testAddress(4), MetadataGeneration: 7, Side: helper.SideBid, NumBaseLots: 29, PriceLimitTicks: 10000, ReduceOnly: true, ClientOrderID: ^uint64(0), LastValidSlot: 164,
			AccountKeys: []helper.AccountManifest{{Pubkey: testAddress(3)}}, Instructions: []helper.InstructionManifest{{ProgramID: helper.PhoenixProgramID}}}}
	built.Manifest.ComputeBudget = (helper.ComputeBudget{}).Effective()
	payload, _ := json.Marshal(phoenixlive.OrderPayload{Format: "solana_legacy_message", MessageBase64: nativeFixture, MessageSHA256: built.MessageSHA256, OwnerAuthority: testAddress(1), PositionAuthority: testAddress(2), FeePayer: testAddress(3), TraderAccount: trader, ClientOrderID: cid, NumBaseLots: "29", PriceLimitTicks: "10000", LastValidSlot: "164"})
	metadata, _ := json.Marshal(phoenixlive.SigningMetadata{ProtocolVersion: 1, Built: built, Normalized: helper.NormalizedOrder{Symbol: "SOL", Side: helper.SideBid, Amount: "0.29", LimitPrice: "100", NumBaseLots: 29, PriceLimitTicks: 10000, BaseLotDecimals: 2, TickSize: "100", MetadataGeneration: 7}, MinContextSlot: 120})
	now := time.Now().UTC()
	q := &domain.SigningRequest{ID: "order-1", ClientOrderID: cid, Venue: "phoenix", Action: "unwind", Account: testAddress(1), Signer: testAddress(2), Symbol: "SOL", Side: "buy", Amount: .29, Price: 100, LimitPrice: 100, ReduceOnly: true, UnsignedPayload: payload, VenueMetadata: metadata, CreatedAt: now, ExpiresAt: now.Add(30 * time.Second)}
	signed := domain.SignedAction{RequestID: q.ID, ClientOrderID: cid, Venue: "phoenix", SignerAddress: q.Signer, Signature: base58.Encode(ed25519.Sign(testKey(2), message))}
	return q, signed
}

type testBackend struct {
	service   *relayer.Service
	store     *Store
	owner, id string
	sends     atomic.Int32
	mode      atomic.Int32
	statusMu  sync.Mutex
	status    *rpc.SignatureStatus
	fee       atomic.Uint64
}

func (*testBackend) BlockHeight(context.Context, uint64) (uint64, error) { return 100, nil }
func (b *testBackend) FeeForMessage(context.Context, []byte, uint64) (uint64, error) {
	return b.fee.Load(), nil
}
func (*testBackend) Balance(context.Context, string) (uint64, error) { return 1_000_000_000, nil }
func (b *testBackend) CoSignOrder(ctx context.Context, built helper.BuildOrderResult, agent, sig string, slot uint64) ([]byte, error) {
	return b.service.CoSign(ctx, built, agent, sig, slot)
}
func (b *testBackend) SignatureStatus(context.Context, string) (*rpc.SignatureStatus, error) {
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	if b.status == nil {
		return nil, nil
	}
	value := *b.status
	return &value, nil
}
func (b *testBackend) SimulateAndSend(ctx context.Context, packet []byte, _, _ uint64) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	a, err := b.store.Get(ctx, b.owner, b.id)
	if err != nil || a.State != "pending" || a.Signature != base58.Encode(packet[1:65]) {
		return "", errors.New("send happened before durable signature")
	}
	b.sends.Add(1)
	signature := base58.Encode(packet[1:65])
	switch b.mode.Load() {
	case 1:
		return signature, &rpc.AmbiguousSendError{Signature: signature, Cause: errors.New("lost response")}
	case 2:
		return "", rpc.ErrSimulation
	}
	return signature, nil
}
func testService(t *testing.T, history *History) (*Service, *testBackend, *domain.SigningRequest, domain.SignedAction) {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, _ := NewStore(db, db)
	q, signed := requestFixture(t)
	b := &testBackend{store: store, owner: q.Account, id: q.ID}
	b.fee.Store(10000)
	signer, err := relayer.NewSigner(base58.Encode(testKey(3)))
	if err != nil {
		t.Fatal(err)
	}
	b.service, err = relayer.NewService(signer, b, relayer.Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(store, b, history), b, q, signed
}
func admit(ctx context.Context, _ *domain.SigningRequest, _ Prepared) (Admission, error) {
	return Admission{Before: Before{BaseLots: "-29", Generation: 1, Slot: 100, Sequence: 41, ObservedAt: time.Now()}, MinSlot: 120, Check: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
}

func TestSubmitCommitsSignatureBeforeSendAndDoesNotResubmitAfterRecreation(t *testing.T) {
	s, b, q, signed := testService(t, nil)
	result, err := s.Submit(context.Background(), q, signed, admit)
	if err != nil || !result.Accepted || result.OrderID == "" || b.sends.Load() != 1 {
		t.Fatal(result, err)
	}
	recreated := NewService(s.store, b, nil)
	result, err = recreated.Submit(context.Background(), q, signed, admit)
	if err != nil || !result.Accepted || b.sends.Load() != 1 {
		t.Fatal("recreated service resent packet", result, err)
	}
}

func TestAmbiguousSendAndConfirmationWithoutHistoryRemainUncertain(t *testing.T) {
	s, b, q, signed := testService(t, NewHistory("http://invalid.test", nil))
	b.mode.Store(1)
	result, err := s.Submit(context.Background(), q, signed, admit)
	if !errors.Is(err, ErrUncertain) || result.OrderID == "" {
		t.Fatal(result, err)
	}
	_, err = NewService(s.store, b, s.history).Submit(context.Background(), q, signed, admit)
	if !errors.Is(err, ErrUncertain) || b.sends.Load() != 1 {
		t.Fatal("ambiguous packet was resent", err)
	}
	if _, err := s.Reconcile(context.Background(), q.Account, q.ID, nil); !errors.Is(err, ErrUncertain) {
		t.Fatal("unknown signature resolved", err)
	}
	b.statusMu.Lock()
	b.status = &rpc.SignatureStatus{Slot: 130, ConfirmationStatus: "finalized", Err: json.RawMessage("null")}
	b.statusMu.Unlock()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":[],"hasMore":false}`)) }))
	defer server.Close()
	s.history = NewHistory(server.URL, server.Client())
	if _, err := s.Reconcile(context.Background(), q.Account, q.ID, func(context.Context, uint64) error { return nil }); !errors.Is(err, ErrUncertain) {
		t.Fatal("RPC confirmation/empty history became a fill", err)
	}
	if a, _ := s.store.Get(context.Background(), q.Account, q.ID); a.State != "pending" {
		t.Fatal(a.State)
	}
}

func TestSimulationRejectionSignatureAndFeeFailuresNeverBecomeAccepted(t *testing.T) {
	for _, kind := range []string{"signature", "fee", "simulation"} {
		t.Run(kind, func(t *testing.T) {
			s, b, q, signed := testService(t, nil)
			switch kind {
			case "signature":
				signed.Signature = base58.Encode(bytes.Repeat([]byte{1}, 64))
			case "fee":
				b.fee.Store(10001)
			case "simulation":
				b.mode.Store(2)
			}
			result, err := s.Submit(context.Background(), q, signed, admit)
			if err != nil || result.Accepted {
				t.Fatal(result, err)
			}
			if kind == "simulation" {
				a, err := s.store.Get(context.Background(), q.Account, q.ID)
				if err != nil || a.State != "failed" {
					t.Fatal(a, err)
				}
			} else if b.sends.Load() != 0 {
				t.Fatal("invalid order reached sender")
			}
		})
	}
}

func TestConcurrentDuplicateSubmissionsSendOnce(t *testing.T) {
	s, b, q, signed := testService(t, nil)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.Submit(context.Background(), q, signed, admit)
			if err != nil || !result.Accepted {
				t.Error(result, err)
			}
		}()
	}
	wg.Wait()
	if b.sends.Load() != 1 {
		t.Fatal("duplicate sends", b.sends.Load())
	}
}

func TestPendingAttemptBlocksAnotherOpenAtomicallyButAllowsSafety(t *testing.T) {
	s, _, q, _ := testService(t, nil)
	first := Attempt{Request: *q, Signature: "sig-1", CreatedAt: time.Now()}
	if winner, err := s.store.Claim(context.Background(), first); err != nil || !winner {
		t.Fatal(winner, err)
	}
	second := first
	second.Request.ID = "order-2"
	second.Request.ClientOrderID = "42"
	second.Signature = "sig-2"
	second.Request.Action = "open"
	if winner, err := s.store.Claim(context.Background(), second); err != nil || winner {
		t.Fatal("pending exposure admitted another open", winner, err)
	}
	second.Request.Action = "emergency_close"
	if winner, err := s.store.Claim(context.Background(), second); err != nil || !winner {
		t.Fatal("pending exposure blocked safety", winner, err)
	}
}

func TestExpiredPendingAttemptAndPostSendPersistenceFailureNeverResendOrReportSuccess(t *testing.T) {
	t.Run("expired pending", func(t *testing.T) {
		s, b, q, signed := testService(t, nil)
		q.CreatedAt = time.Now().UTC().Add(-time.Minute)
		q.ExpiresAt = q.CreatedAt.Add(30 * time.Second)
		sig := base58.Encode(ed25519.Sign(testKey(3), mustMessage(t)))
		if winner, err := s.store.Claim(context.Background(), Attempt{Request: *q, Signature: sig, CreatedAt: q.CreatedAt}); err != nil || !winner {
			t.Fatal(winner, err)
		}
		if _, err := s.Submit(context.Background(), q, signed, admit); !errors.Is(err, ErrUncertain) || b.sends.Load() != 0 {
			t.Fatal("expired pending attempt was replaced/resent", err)
		}
	})
	t.Run("post-send DB failure", func(t *testing.T) {
		s, b, q, signed := testService(t, nil)
		if _, err := s.store.writer.Exec(`CREATE TRIGGER reject_order_outcome BEFORE UPDATE ON phoenix_order_attempts BEGIN SELECT RAISE(ABORT,'scripted outcome failure'); END`); err != nil {
			t.Fatal(err)
		}
		if result, err := s.Submit(context.Background(), q, signed, admit); !errors.Is(err, ErrUncertain) || result != nil || b.sends.Load() != 1 {
			t.Fatal("unpersisted acceptance reported success", result, err)
		}
		if a, err := s.store.Get(context.Background(), q.Account, q.ID); err != nil || a.State != "pending" {
			t.Fatal(a, err)
		}
		if _, err := NewService(s.store, b, nil).Submit(context.Background(), q, signed, admit); !errors.Is(err, ErrUncertain) || b.sends.Load() != 1 {
			t.Fatal("DB failure caused resend", err)
		}
	})
}
func mustMessage(t *testing.T) []byte {
	t.Helper()
	message, err := base64.StdEncoding.DecodeString(nativeFixture)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
