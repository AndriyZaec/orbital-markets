export interface SigningRequest {
  id: string
  client_order_id: string
  venue: 'pacifica' | 'hyperliquid'
  action: 'open' | 'close'
  symbol: string
  side: 'buy' | 'sell'
  amount: number
  price: number
  reduce_only: boolean
  unsigned_payload: unknown
  venue_metadata?: unknown
  expires_at: string
  created_at: string
}

export interface SignedAction {
  request_id: string
  client_order_id: string
  venue: string
  signer_address: string
  signature: string
}

export interface SubmissionResult {
  request_id: string
  client_order_id: string
  venue: string
  order_id?: string
  accepted: boolean
  error?: string
  submitted_at: string
  responded_at: string
}

export interface PrepareRequest {
  opportunity_id: string
  leverage: number
  account_pacifica: string
  account_hyperliquid: string
}

export interface PrepareResponse {
  plan_id: string
  asset: string
  notional: number
  leverage: { leverage: number; [k: string]: unknown }
  signing_requests: SigningRequest[]
}
