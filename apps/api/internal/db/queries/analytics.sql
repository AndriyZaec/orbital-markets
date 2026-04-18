-- name: AnalyticsSummary :one
SELECT
    COUNT(*) AS total_trades,
    COUNT(CASE WHEN state = 'closed' THEN 1 END) AS closed_trades,
    COUNT(CASE WHEN state IN ('open', 'degraded') THEN 1 END) AS open_trades,
    COUNT(CASE WHEN state = 'failed' THEN 1 END) AS failed_trades,
    COALESCE(SUM(price_pnl), 0) AS total_price_pnl,
    COALESCE(SUM(funding_pnl), 0) AS total_funding_pnl,
    COALESCE(SUM(total_pnl), 0) AS total_pnl,
    COALESCE(SUM(CASE WHEN state = 'closed' THEN realized_pnl END), 0) AS total_realized_pnl,
    COALESCE(SUM(CASE WHEN state IN ('open', 'degraded') THEN total_pnl END), 0) AS total_unrealized_pnl,
    COALESCE(AVG(CASE WHEN state = 'closed' THEN hold_hours END), 0) AS avg_hold_hours,
    COALESCE(AVG(CASE WHEN state = 'closed' THEN est_break_even_hours END), 0) AS avg_est_break_even_hours,
    COUNT(CASE WHEN state = 'closed' AND break_even_reached = 1 THEN 1 END) AS break_even_reached_count,
    COUNT(CASE WHEN state = 'closed' AND break_even_reached = 0 THEN 1 END) AS break_even_not_reached_count,
    COUNT(CASE WHEN state = 'closed' AND realized_pnl > 0 THEN 1 END) AS profitable_trades,
    COUNT(CASE WHEN state = 'closed' AND realized_pnl <= 0 THEN 1 END) AS unprofitable_trades
FROM paper_positions;

-- name: AnalyticsByAsset :many
SELECT
    asset,
    COUNT(*) AS total_trades,
    COUNT(CASE WHEN state = 'closed' THEN 1 END) AS closed_trades,
    COALESCE(SUM(price_pnl), 0) AS total_price_pnl,
    COALESCE(SUM(funding_pnl), 0) AS total_funding_pnl,
    COALESCE(SUM(total_pnl), 0) AS total_pnl,
    COALESCE(SUM(CASE WHEN state = 'closed' THEN realized_pnl END), 0) AS total_realized_pnl,
    COALESCE(SUM(CASE WHEN state IN ('open', 'degraded') THEN total_pnl END), 0) AS total_unrealized_pnl,
    COALESCE(AVG(CASE WHEN state = 'closed' THEN hold_hours END), 0) AS avg_hold_hours,
    COALESCE(AVG(CASE WHEN state = 'closed' THEN est_break_even_hours END), 0) AS avg_est_break_even_hours,
    COUNT(CASE WHEN state = 'closed' AND break_even_reached = 1 THEN 1 END) AS break_even_reached_count,
    COUNT(CASE WHEN state = 'closed' AND break_even_reached = 0 THEN 1 END) AS break_even_not_reached_count,
    COUNT(CASE WHEN state = 'closed' AND realized_pnl > 0 THEN 1 END) AS profitable_trades
FROM paper_positions
WHERE asset != ''
GROUP BY asset
ORDER BY total_pnl DESC;

-- name: AnalyticsByRiskTier :many
SELECT
    risk_tier,
    COUNT(*) AS total_trades,
    COUNT(CASE WHEN state = 'closed' THEN 1 END) AS closed_trades,
    COALESCE(SUM(price_pnl), 0) AS total_price_pnl,
    COALESCE(SUM(funding_pnl), 0) AS total_funding_pnl,
    COALESCE(SUM(total_pnl), 0) AS total_pnl,
    COALESCE(SUM(CASE WHEN state = 'closed' THEN realized_pnl END), 0) AS total_realized_pnl,
    COALESCE(AVG(CASE WHEN state = 'closed' THEN hold_hours END), 0) AS avg_hold_hours,
    COUNT(CASE WHEN state = 'closed' AND break_even_reached = 1 THEN 1 END) AS break_even_reached_count,
    COUNT(CASE WHEN state = 'closed' AND realized_pnl > 0 THEN 1 END) AS profitable_trades
FROM paper_positions
WHERE risk_tier != ''
GROUP BY risk_tier
ORDER BY risk_tier;

-- name: AnalyticsByCloseReason :many
SELECT
    close_reason,
    COUNT(*) AS total_trades,
    COALESCE(SUM(price_pnl), 0) AS total_price_pnl,
    COALESCE(SUM(funding_pnl), 0) AS total_funding_pnl,
    COALESCE(SUM(realized_pnl), 0) AS total_realized_pnl,
    COALESCE(AVG(hold_hours), 0) AS avg_hold_hours,
    COUNT(CASE WHEN realized_pnl > 0 THEN 1 END) AS profitable_trades
FROM paper_positions
WHERE state = 'closed' AND close_reason != ''
GROUP BY close_reason
ORDER BY total_trades DESC;
