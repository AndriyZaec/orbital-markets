import bs58 from 'bs58'

export async function signPacifica(
  unsignedPayload: unknown,
  signMessage: (message: Uint8Array) => Promise<Uint8Array>,
): Promise<string> {
  const order = unsignedPayload as Record<string, unknown>
  const canonicalPayload = {
    data: {
      amount: order.amount,
      client_order_id: order.client_order_id,
      reduce_only: order.reduce_only,
      side: order.side,
      slippage_percent: order.slippage_percent,
      symbol: order.symbol,
    },
    expiry_window: order.expiry_window,
    timestamp: order.timestamp,
    type: 'create_market_order',
  }
  const payloadBytes = new TextEncoder().encode(JSON.stringify(canonicalPayload))
  const signature = await signMessage(payloadBytes)
  return bs58.encode(signature)
}
