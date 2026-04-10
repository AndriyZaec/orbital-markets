use std::net::SocketAddr;
use std::sync::{Arc, RwLock};
use std::time::Duration;

use gmsol_sdk::market::MarketCalculations;
use gmsol_sdk::model::MarketModel;
use gmsol_sdk::solana_utils::solana_sdk::signature::Keypair;
use gmsol_sdk::Client;
use http_body_util::Full;
use hyper::body::Bytes;
use hyper::service::service_fn;
use hyper::{Request, Response, StatusCode};
use hyper_util::rt::TokioIo;
use hyper_util::server::conn::auto::Builder;
use serde::Serialize;
use tokio::net::TcpListener;
use tracing::{error, info, warn};

#[derive(Debug, Clone, Serialize)]
struct MarketData {
    venue: String,
    asset: String,
    market_key: String,
    mark_price: f64,
    index_price: f64,
    funding_rate: f64,
    bid_price: f64,
    ask_price: f64,
    open_interest: f64,
    timestamp: String,
}

type SharedState = Arc<RwLock<Vec<MarketData>>>;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();

    let state: SharedState = Arc::new(RwLock::new(Vec::new()));

    let poll_state = state.clone();
    tokio::spawn(async move {
        if let Err(e) = poll_loop(poll_state).await {
            error!("poll loop failed: {e}");
        }
    });

    let addr: SocketAddr = std::env::var("ADDR")
        .unwrap_or_else(|_| "127.0.0.1:8081".to_string())
        .parse()?;

    let listener = TcpListener::bind(addr).await?;
    info!("gmtrade sidecar listening on {addr}");

    loop {
        let (stream, _) = listener.accept().await?;
        let state = state.clone();
        tokio::spawn(async move {
            let service = service_fn(move |req| {
                let state = state.clone();
                async move { handle(req, state) }
            });
            if let Err(e) = Builder::new(hyper_util::rt::TokioExecutor::new())
                .serve_connection(TokioIo::new(stream), service)
                .await
            {
                error!("connection error: {e}");
            }
        });
    }
}

fn handle(
    req: Request<hyper::body::Incoming>,
    state: SharedState,
) -> Result<Response<Full<Bytes>>, hyper::Error> {
    match req.uri().path() {
        "/markets" => {
            let data = state.read().unwrap();
            let json = serde_json::to_vec(&*data).unwrap_or_default();
            Ok(Response::builder()
                .status(StatusCode::OK)
                .header("content-type", "application/json")
                .body(Full::new(Bytes::from(json)))
                .unwrap())
        }
        "/health" => Ok(Response::builder()
            .status(StatusCode::OK)
            .header("content-type", "application/json")
            .body(Full::new(Bytes::from(r#"{"status":"ok"}"#)))
            .unwrap()),
        _ => Ok(Response::builder()
            .status(StatusCode::NOT_FOUND)
            .body(Full::new(Bytes::from("not found")))
            .unwrap()),
    }
}

async fn poll_loop(state: SharedState) -> anyhow::Result<()> {
    let rpc_url = std::env::var("RPC_URL")
        .unwrap_or_else(|_| "https://api.mainnet-beta.solana.com".to_string());

    let payer = Keypair::new();
    let cluster = rpc_url
        .parse()
        .unwrap_or(gmsol_sdk::solana_utils::cluster::Cluster::Mainnet);
    let client = Client::new(cluster, &payer)?;
    let http = reqwest::Client::new();

    let store = client.find_store_address("");
    info!("starting poll loop, store={store}");

    loop {
        match fetch_markets(&client, &http, &store).await {
            Ok(markets) => {
                let count = markets.len();
                let mut w = state.write().unwrap();
                *w = markets;
                info!("updated {count} markets");
            }
            Err(e) => {
                warn!("fetch failed: {e}");
            }
        }
        tokio::time::sleep(Duration::from_secs(5)).await;
    }
}

async fn fetch_markets(
    client: &Client<Keypair>,
    http: &reqwest::Client,
    store: &gmsol_sdk::solana_utils::solana_sdk::pubkey::Pubkey,
) -> anyhow::Result<Vec<MarketData>> {
    let markets = client.markets(store).await?;
    let token_map = client.authorized_token_map(store).await?;

    let now = chrono::Utc::now().to_rfc3339();
    let mut result = Vec::new();

    for (address, market) in markets.iter() {
        let name = match market.name() {
            Ok(n) => n.to_string(),
            Err(_) => continue,
        };

        // Fetch mint for supply
        let mint_address = &market.meta.market_token_mint;
        let mint = match client
            .account::<gmsol_sdk::solana_utils::anchor_lang::prelude::Mint>(mint_address)
            .await?
        {
            Some(m) => m,
            None => continue,
        };

        let model = MarketModel::from_parts((**market).clone(), mint.supply);

        // Get prices from Hermes directly
        let prices = match fetch_prices_for_market(http, &token_map, &**market).await {
            Ok(p) => p,
            Err(e) => {
                warn!("skip {name}: prices unavailable: {e}");
                continue;
            }
        };

        let status = match model.status(&prices) {
            Ok(s) => s,
            Err(e) => {
                warn!("skip {name}: status error: {e}");
                continue;
            }
        };

        let funding_long = status.funding_rate_per_second_for_long as f64 / 1e30 * 3600.0;

        let index_price = prices.index_token_price.max as f64 / 1e30;
        let index_price_min = prices.index_token_price.min as f64 / 1e30;

        let oi_long = status.open_interest_for_long as f64 / 1e30;
        let oi_short = status.open_interest_for_short as f64 / 1e30;

        result.push(MarketData {
            venue: "gmtrade".to_string(),
            asset: name.clone(),
            market_key: address.to_string(),
            mark_price: index_price,
            index_price: index_price_min,
            funding_rate: funding_long,
            bid_price: index_price_min,
            ask_price: index_price,
            open_interest: oi_long + oi_short,
            timestamp: now.clone(),
        });
    }

    Ok(result)
}

/// Fetch prices from Pyth Hermes HTTP API for a market's tokens.
async fn fetch_prices_for_market(
    http: &reqwest::Client,
    _token_map: &gmsol_sdk::client::token_map::TokenMapAccess,
    _market: &gmsol_sdk::store::market::Market,
) -> anyhow::Result<gmsol_model::price::Prices<u128>> {
    // TODO: extract Pyth feed IDs from token_map for this market's tokens,
    // call Hermes API, and convert to gmsol_model::price::Prices
    //
    // Hermes endpoint:
    //   GET https://hermes.pyth.network/v2/updates/price/latest?ids[]=<feed_id>
    //
    // For now, return error to skip markets until we map feed IDs
    anyhow::bail!("hermes price fetch not yet implemented")
}
