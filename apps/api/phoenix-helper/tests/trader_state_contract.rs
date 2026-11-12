//! Synthetic Go reducer fixtures must also decode against the pinned SDK wire
//! types. This uses the lightweight types feature, not the unavailable API client.
use phoenix_rise::types::prelude::{TraderStatePayload, TraderStateServerMessage};
use serde_json::{Value, json};

#[test]
fn account_fixtures_match_pinned_rise_wire_types() {
    let rest: Value = serde_json::from_str(include_str!(
        "../../internal/venue/phoenix/account/testdata/trader-snapshot.json"
    ))
    .unwrap();
    let mut snapshot = rest["snapshot"].clone();
    for key in ["authority", "traderPdaIndex", "slot"] {
        snapshot[key] = rest[key].clone();
    }
    snapshot["messageType"] = json!("snapshot");
    let snapshot: TraderStateServerMessage = serde_json::from_value(snapshot).unwrap();
    let TraderStatePayload::Snapshot(snapshot) = snapshot.content else {
        panic!("expected snapshot");
    };
    assert_eq!(snapshot.subaccounts[0].sequence, 41);
    assert_eq!(
        snapshot.subaccounts[0].spot_collaterals[0].balance,
        "1000000001"
    );
    assert_eq!(
        snapshot.subaccounts[0].positions[0]
            .position
            .base_position_lots,
        "-9007199254740993"
    );

    let delta: TraderStateServerMessage = serde_json::from_str(include_str!(
        "../../internal/venue/phoenix/account/testdata/trader-delta.json"
    ))
    .unwrap();
    let TraderStatePayload::Delta(delta) = delta.content else {
        panic!("expected delta");
    };
    assert_eq!(delta.deltas[0].sequence, 42);
    assert_eq!(delta.deltas[0].positions.len(), 2);
}
