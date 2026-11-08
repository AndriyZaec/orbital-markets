package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	phoenixlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	phoenixruntime "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/runtime"
	"github.com/mr-tron/base58"
)

type phoenixSubmitTestBackend struct {
	*phoenixOrderTestBackend
	relayer            *relayer.Service
	journal            *orders.Store
	request            *domain.SigningRequest
	sends              atomic.Int32
	afterCoSign        func()
	denyInfrastructure atomic.Bool
	readinessCalls     atomic.Int32
}

func (b *phoenixSubmitTestBackend) Readiness(context.Context) phoenixruntime.Status {
	b.readinessCalls.Add(1)
	return phoenixruntime.Status{Ready: !b.denyInfrastructure.Load(), Reason: "test_infrastructure"}
}
func (b *phoenixSubmitTestBackend) LatestBlockhash(ctx context.Context) (rpc.Blockhash, error) {
	if b.denyInfrastructure.Load() {
		return rpc.Blockhash{}, errors.New("RPC unavailable")
	}
	return b.phoenixOrderTestBackend.LatestBlockhash(ctx)
}
func (b *phoenixSubmitTestBackend) FlightReadiness() phoenixruntime.FlightStatus {
	return phoenixruntime.FlightStatus{Ready: !b.denyFlight.Load(), BuilderAuthority: b.BuilderAuthority()}
}

func (*phoenixSubmitTestBackend) BlockHeight(context.Context, uint64) (uint64, error) {
	return 100, nil
}
func (*phoenixSubmitTestBackend) FeeForMessage(context.Context, []byte, uint64) (uint64, error) {
	return 10000, nil
}
func (*phoenixSubmitTestBackend) Balance(context.Context, string) (uint64, error) {
	return 1_000_000_000, nil
}
func (b *phoenixSubmitTestBackend) Address() string { return b.relayer.Address() }
func (b *phoenixSubmitTestBackend) CoSignOrder(ctx context.Context, q helper.BuildOrderResult, agent, sig string, slot uint64) ([]byte, error) {
	packet, err := b.relayer.CoSign(ctx, q, agent, sig, slot)
	if b.afterCoSign != nil {
		b.afterCoSign()
	}
	return packet, err
}
func (b *phoenixSubmitTestBackend) SignatureStatus(context.Context, string) (*rpc.SignatureStatus, error) {
	return nil, nil
}
func (b *phoenixSubmitTestBackend) SimulateAndSend(ctx context.Context, packet []byte, _, _ uint64) (string, error) {
	a, err := b.journal.Get(ctx, b.request.Account, b.request.ID)
	if err != nil || a.State != "pending" || a.Signature != base58.Encode(packet[1:65]) {
		return "", errors.New("missing durable attempt")
	}
	b.sends.Add(1)
	return a.Signature, nil
}

func enablePhoenixSubmitTestBackend(t *testing.T, f *phoenixAccountFixture, base *phoenixOrderTestBackend) *phoenixSubmitTestBackend {
	t.Helper()
	agentKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))
	agent := base58.Encode(agentKey.Public().(ed25519.PublicKey))
	f.backend.agent = agent
	if err := f.store.UpsertAgentAuthorization(context.Background(), "phoenix", phoenixTestOwner, agent); err != nil {
		t.Fatal(err)
	}
	b := &phoenixSubmitTestBackend{phoenixOrderTestBackend: base, journal: f.factory.orderJournal}
	signer, err := relayer.NewSigner(base58.Encode(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32))))
	if err != nil {
		t.Fatal(err)
	}
	b.relayer, err = relayer.NewService(signer, b, relayer.Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	f.factory.config.Backend = b
	f.factory.orders = orders.NewService(b.journal, b, orders.NewHistory(f.factory.config.RESTURL, f.factory.config.HTTPClient))
	return b
}

func TestPhoenixFeedSubmitsExactSDKPacketOnceAndNeverTurnsAcceptanceIntoFill(t *testing.T) {
	agentKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))
	agent := base58.Encode(agentKey.Public().(ed25519.PublicKey))
	var b *phoenixSubmitTestBackend
	f, _, _, _, feed := newPhoenixPrepareFixture(t, func(f *phoenixAccountFixture, base *phoenixOrderTestBackend, _ *phoenixOrderTestMarkets) {
		b = enablePhoenixSubmitTestBackend(t, f, base)
	})
	module, err := phoenixlive.NewLiveModule(context.Background(), &phoenixOrderPreparer{deps: f.server.live, config: f.factory.config, backend: b})
	if err != nil {
		t.Fatal(err)
	}
	params := phoenixPrepareParams()
	params.Signer = agent
	q, err := module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: params, Action: venue.ReduceActionUnwind})
	if err != nil {
		t.Fatal(err)
	}
	b.request = q
	var metadata phoenixlive.SigningMetadata
	if err := json.Unmarshal(q.VenueMetadata, &metadata); err != nil {
		t.Fatal(err)
	}
	message, _ := base64.StdEncoding.DecodeString(metadata.Built.MessageBase64)
	signed := domain.SignedAction{RequestID: q.ID, ClientOrderID: q.ClientOrderID, Venue: "phoenix", SignerAddress: agent, Signature: base58.Encode(ed25519.Sign(agentKey, message))}
	result, err := feed.SubmitSigned(context.Background(), signed, q)
	if err != nil || !result.Accepted || result.OrderID == "" || b.sends.Load() != 1 {
		t.Fatal(result, err)
	}
	if _, err := feed.SubmitSigned(context.Background(), signed, q); err != nil || b.sends.Load() != 1 {
		t.Fatal("feed resubmitted packet", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if fill, err := feed.WaitForFill(ctx, q); !errors.Is(err, orders.ErrUncertain) || fill != nil {
		t.Fatal("acceptance became a fill", fill, err)
	}
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal(err)
	}
	if feed.Snapshot().Phoenix.LiveReady || !supportedLiveVenue("phoenix") {
		t.Fatal("unresolved submission did not block new opening")
	}
	if err := feed.WaitForLeverage(context.Background(), "SOL", 5); err != nil {
		t.Fatal("unsupported leverage was not a no-op", err)
	}
}

func TestPhoenixSubmitRechecksAuthorityAfterPassiveReadiness(t *testing.T) {
	for _, action := range []string{"open", "close", "unwind", "emergency_close"} {
		t.Run(action, func(t *testing.T) {
			var b *phoenixSubmitTestBackend
			f, _, _, _, feed := newPhoenixPrepareFixture(t, func(f *phoenixAccountFixture, base *phoenixOrderTestBackend, _ *phoenixOrderTestMarkets) {
				b = enablePhoenixSubmitTestBackend(t, f, base)
			})
			module, err := phoenixlive.NewLiveModule(context.Background(), &phoenixOrderPreparer{deps: f.server.live, config: f.factory.config, backend: b})
			if err != nil {
				t.Fatal(err)
			}
			params := phoenixPrepareParams()
			params.Signer = f.backend.agent
			var q *domain.SigningRequest
			if action == "open" {
				q, err = module.BuildOpen(params)
			} else {
				q, err = module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: params, Action: venue.ReduceAction(action)})
			}
			if err != nil {
				t.Fatal(err)
			}
			f.backend.mu.Lock()
			f.backend.agent = phoenixTestOwner // Replaced outside Orbital after preparation.
			f.backend.mu.Unlock()
			before := f.backend.authorityCalls.Load()
			if err := feed.refreshView(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !feed.Snapshot().Phoenix.LiveReady || f.backend.authorityCalls.Load() != before {
				t.Fatal("passive refresh performed a chain admission check")
			}
			var metadata phoenixlive.SigningMetadata
			if err := json.Unmarshal(q.VenueMetadata, &metadata); err != nil {
				t.Fatal(err)
			}
			message, _ := base64.StdEncoding.DecodeString(metadata.Built.MessageBase64)
			key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))
			signed := domain.SignedAction{RequestID: q.ID, ClientOrderID: q.ClientOrderID, Venue: "phoenix", SignerAddress: params.Signer, Signature: base58.Encode(ed25519.Sign(key, message))}
			result, err := feed.SubmitSigned(context.Background(), signed, q)
			if err != nil || result == nil || result.Accepted || result.Error != "Phoenix current agent/slot differs from signed order" || b.sends.Load() != 0 || f.backend.authorityCalls.Load() <= before {
				t.Fatal("submission did not reject on fresh authority evidence", result, err)
			}
		})
	}
}
