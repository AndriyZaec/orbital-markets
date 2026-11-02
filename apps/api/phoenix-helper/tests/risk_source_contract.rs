//! Synthetic risk-source fixtures use the public API shape, checked against the
//! pinned SDK DTOs, including the canonical collateral-to-pricing-market join.
use phoenix_rise::types::exchange::{CollateralAssetsResponse, ExchangeMarketConfig};
use serde::Deserialize;

#[test]
fn full_parameters_and_collateral_match_pinned_sdk() {
    #[derive(Deserialize)]
    struct Catalog {
        markets: Vec<ExchangeMarketConfig>,
    }
    let catalog: Catalog = serde_json::from_str(include_str!(
        "../../internal/venue/phoenix/risk/testdata/parameters.json"
    ))
    .unwrap();
    let assets: CollateralAssetsResponse = serde_json::from_str(include_str!(
        "../../internal/venue/phoenix/risk/testdata/collateral.json"
    ))
    .unwrap();
    assert_eq!(catalog.markets[0].leverage_tiers.len(), 4);
    assert_eq!(catalog.markets[0].risk_factors.maintenance_bps, Some(5000));
    let btc = assets
        .assets
        .iter()
        .find(|asset| asset.symbol == "cbBTC")
        .unwrap();
    let params = btc
        .margin_params(|id| {
            catalog
                .markets
                .iter()
                .find(|m| m.asset_id == id)
                .map(|m| m.symbol.clone())
        })
        .unwrap()
        .unwrap();
    assert_eq!(params.perp_symbol, "BTC");
    assert_eq!(params.decimals, 8);
}
