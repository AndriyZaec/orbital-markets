# Aster Reference Leverage And Admin APR Reliability

## Objective

Finish account-independent Aster discovery without showing false per-market
bracket errors, add an optional backend-owned read-only reference bracket cache,
and restore reliable hourly data for the admin Weekly APR report.

This branch must preserve the existing fail-closed execution boundary: shared
reference leverage is an estimate for discovery only. A selected user's own
Aster account state and leverage brackets remain authoritative for plan building
and `/live/prepare`.

## Confirmed Production Problems

### False Aster bracket errors

The public Aster market adapter does not publish `MarketData.MaxLeverage`.
Shared opportunity enrichment interpreted that zero value as
`missing/bracket_missing`, so every Aster row displayed `Aster has no bracket for
this market` even though no account-specific bracket query had failed.

When no fresh shared reference snapshot exists, discovery must omit the Aster
leverage estimate instead of presenting a per-market error.

### SQLite writer contention

Production logs repeatedly report `SQLITE_BUSY` from snapshot recording, live
monitoring, and funding-history persistence. The API uses one SQLite file and
one process but configures an eight-connection mixed read/write pool. WAL permits
concurrent readers, not concurrent writers.

Failed recorder or rollup writes create gaps in `market_snapshots_1h`. Weekly APR
then silently aggregates only the remaining joined venue samples. Analytics
handlers also returned generic 500 responses without logging the underlying
database error.

### Missing shared Aster reference source

The existing backend data-agent implementation can already:

- generate and encrypt a backend-held delegated key;
- request owner approval with `canSpotTrade=false`, `canPerpTrade=false`, and
  `canWithdraw=false`;
- verify that the approved agent has read permission and no write permission;
- sign allowlisted GET requests only;
- read `/fapi/v3/leverageBracket` without a symbol to obtain all brackets.

What was missing was a dedicated reference-account lifecycle, an immutable
background cache, and discovery-only integration.

## Product Decisions

- `GET /api/v1/opportunities` remains account-independent.
- Aster reference leverage is optional and informative.
- A missing, loading, failed, or expired global reference source is represented
  as `unsupported/reference_unavailable`; the table omits the estimate.
- A fresh reference snapshot that lacks one exact market symbol may report a
  genuine per-market `missing/bracket_missing` capability.
- Reference refreshes run in the background and are never triggered by an
  opportunities request.
- The reference owner uses a dedicated empty Aster account.
- Its OrbitalData key is generated and encrypted by the existing backend
  data-agent flow. Only the owner address is configured as
  `ASTER_REFERENCE_ACCOUNT`.
- Operational SQLite access is serialized through one `database/sql`
  connection. Analytics uses a separate single-connection, query-only reader so
  internal reports cannot occupy the money-sensitive write queue.
- Admin Weekly APR keeps its current formula and UTC calendar-week grouping.

## Safety Invariants

- The reference data agent must have `canRead=true` and no spot, perp, or
  withdrawal permission.
- Never log a private key, signature, full owner address, signed payload, cookie,
  or unbounded venue response.
- Never pass the reference cache or reference owner into live account feeds,
  order lookup, funding reconciliation, recovery, or execution signing.
- Browser/reference leverage must never authorize a live order.
- `/live/prepare` must refresh and validate the selected user's exact account,
  symbol, notional tier, leverage, and execution-agent authorization.
- If the reference cache is unavailable, discovery remains usable and live
  execution remains fail-closed on user-specific state.
- A failed cache refresh cannot replace or extend the previous successful
  generation.
- No production database export or credential may enter the repository.

## Workstream 1: Correct Discovery Fallback

- Classify absent Aster reference metadata as
  `unsupported/reference_unavailable`, not `missing/bracket_missing`.
- Omit leverage text and leveraged APR from table rows when no estimate exists.
- Keep generic availability visible in selected execution detail.
- Add API and frontend regression tests for the exact false-error symptom.

## Workstream 2: Stabilize SQLite And Admin APR

- Restore `SetMaxOpenConns(1)` and `SetMaxIdleConns(1)` while retaining WAL,
  foreign keys, and the five-second busy timeout.
- Route analytics through a separate `mode=ro`, `query_only` WAL connection with
  a short busy timeout; fail analytics closed if that isolation is unavailable.
- Use passive WAL checkpoints so an active analytics snapshot never makes the
  operational connection wait for truncation.
- Add a regression test for the connection and PRAGMA invariants.
- Log underlying errors for live analytics, Weekly APR, and public metrics while
  retaining bounded client-facing errors.
- Verify recorder, rollup, opportunity signals, and Weekly APR together under
  the serialized pool.
- Follow up with bounded recent-bucket rollup repair if production evidence
  still shows gaps after writer serialization.

## Workstream 3: Read All Reference Brackets

- Add `Service.ReadAllLeverageBrackets(ctx, owner)` to the existing Aster
  data-agent service.
- Reuse the signed-read allowlist, monotonic nonce, response-size limit,
  encrypted credential store, and `ParseLeverageBrackets` validation.
- Send no `symbol` parameter for the all-market request.
- Re-check the current venue agent report before every all-market read and reject
  any generation unless read permission is present, all write permissions are
  absent, and the venue-reported authorization has not expired.
- Reject empty or malformed all-market responses.

## Workstream 4: Immutable Reference Cache

The cache owns one complete generation:

```text
brackets_by_market_key
observed_at
expires_at
revision
source_status
```

- Refresh immediately in the background and every two minutes.
- Use a five-minute freshness TTL.
- Coalesce concurrent refresh calls.
- Atomically replace the full generation only after a complete successful read.
- Preserve a fresh previous generation during transient failure.
- Immediately invalidate numeric capabilities if a refresh detects missing read
  permission, any write permission, or an expired reference authorization.
- Stop serving numeric estimates after expiry.
- Calculate tiers by exact Aster `MarketKey` and requested reference notional.
- Publish bracket revision and freshness timestamps with known capabilities.
- Keep source health logging bounded to status, symbol count, revision, and
  categorized error; never include the owner.

## Workstream 5: Shared Opportunity Integration

- Capture one cache snapshot per opportunities response.
- Resolve Aster by the exact scanner `MarketKey`, not by deriving `asset+USDT`.
- Always use the tiered reference generation for Aster, even if Aster later adds
  a non-zero public venue-wide maximum; continue using public
  `MarketData.MaxLeverage` for other venues.
- Compute pair max leverage only when both public/reference capabilities are
  known.
- Do not mutate scanner market snapshots with reference values.
- Reject the configured reference owner from generic live account/funding/order
  readers and from externally callable data-agent lifecycle endpoints.
- Prove that account query parameters remain ignored and HTTP requests never
  trigger Aster signed reads.

## Reference Account Provisioning

1. Create a dedicated empty Aster owner account.
2. Generate an OrbitalData agent through the existing backend prepare flow.
3. Sign only the read-only approval:
   - `canSpotTrade=false`
   - `canPerpTrade=false`
   - `canWithdraw=false`
4. Submit and validate the approval; confirm `canRead=true` and no write
   permissions in the venue agent report.
5. Keep the encrypted agent record in SQLite under
   `ASTER_DATA_AGENT_MASTER_KEY`.
6. Configure `ASTER_REFERENCE_ACCOUNT` with the dedicated owner address.
7. Deploy and wait for a successful reference-cache generation before treating
   Aster leverage as available.
8. For rotation, approve the replacement first, verify a fresh generation, then
   revoke the old agent.

If strict read-only permission cannot be independently verified, leave
`ASTER_REFERENCE_ACCOUNT` unset.

## Testing

### Cache

- cold and disabled cache omits reference leverage;
- successful refresh publishes all markets atomically;
- notional boundaries choose the correct tier;
- concurrent refreshes coalesce;
- a failed refresh preserves the fresh prior generation;
- expiry removes numeric capability without producing per-row stale noise;
- recovery increments revision and replaces the generation;
- malformed or empty responses never replace good data;
- race tests pass.

### Discovery and execution boundary

- Aster reference capability uses the exact market key;
- missing reference configuration returns `reference_unavailable`;
- a known reference cap appears only in shared discovery;
- public 20x plus user 8x produces an authoritative live cap of 8x;
- stale or missing user brackets still block live prepare;
- reference availability never changes close, unwind, recovery, or kill switch.

### Admin APR

- the operational SQLite pool is single-connection with WAL, foreign keys, and
  busy timeout, while analytics remains query-only and independently pooled;
- recorder and rollup writes no longer fail with same-process `SQLITE_BUSY`;
- Weekly APR retains peak-direction semantics and top-five ordering;
- analytics query failures produce an operator log and bounded 500 response.

## Verification

```bash
cd apps/api
go test ./...
go test -race ./...
go vet ./...
go build ./...

cd ../web
npm test
npm run lint
npm run build

cd ../admin
pnpm test
pnpm typecheck
pnpm worker:typecheck
pnpm build

cd ../..
git diff --check
```

## Production Rollout

1. Deploy the SQLite/admin fixes without `ASTER_REFERENCE_ACCOUNT`.
2. Confirm no new `SQLITE_BUSY` events through at least one recorder and rollup
   cycle; verify Weekly APR loads repeatedly.
3. Provision and validate the dedicated read-only Aster agent.
4. Set `ASTER_REFERENCE_ACCOUNT` and deploy.
5. Confirm cache refresh logs show a non-zero symbol count and increasing
   revision without owner addresses.
6. Confirm Aster rows show leverage estimates when fresh and omit them during a
   forced reference outage.
7. Confirm a selected Aster live plan still uses the user's lower authoritative
   cap and fails closed when user brackets are unavailable.

## Exit Criteria

- No false `Aster has no bracket for this market` message appears solely because
  public market metadata lacks leverage.
- Shared Aster estimates come from a fresh, read-only reference generation or
  are omitted.
- Reference credentials cannot authorize trading or withdrawals and are never
  used by live execution.
- Production snapshot, rollup, and monitoring writes no longer report
  same-process `SQLITE_BUSY` under normal load.
- Weekly APR consistently returns the expected UTC-week rows.
- Full API, web, admin, race, lint, typecheck, and build verification passes.
