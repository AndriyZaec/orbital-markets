import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useAccount, useChainId, useSignTypedData } from 'wagmi'

import { apiFetch } from '@/lib/api'
import type { SigningRequest } from '@/types/signing'
import {
  authorizeHyperliquidAgent,
  type HyperliquidApproveAgentRequest,
} from './hyperliquid-agent.ts'
import { authorizePacificaAgent, type PacificaBindAgentRequest } from './pacifica-agent.ts'
import { signWithStoredTradingAgent } from './signing.ts'
import {
  clearStoredTradingAgent,
  loadAfterOwnerChange,
  type StorageLike,
} from './storage.ts'
import type { TradingAgentState, Venue } from './types'
import { TradingAgentContext } from './TradingAgentContext'

function missingState(venue: Venue, ownerAddress: string | null): TradingAgentState {
  return { venue, ownerAddress, agentAddress: null, status: 'missing', error: null }
}

function browserStorage(): StorageLike {
  if (typeof window === 'undefined') throw new Error('Trading agents require a browser session')
  return window.sessionStorage
}

export function TradingAgentProvider({ children }: { children: ReactNode }) {
  const solana = useWallet()
  const evm = useAccount()
  const chainId = useChainId()
  const { signTypedDataAsync } = useSignTypedData()
  const pacificaOwner = solana.connected && solana.publicKey ? solana.publicKey.toBase58() : null
  const hyperliquidOwner = evm.isConnected && evm.address ? evm.address : null
  const previousOwners = useRef<{ pacifica: string | null; hyperliquid: string | null }>({
    pacifica: null,
    hyperliquid: null,
  })
  useEffect(() => {
    loadAfterOwnerChange(browserStorage(), 'pacifica', previousOwners.current.pacifica, pacificaOwner)
    previousOwners.current.pacifica = pacificaOwner
  }, [pacificaOwner])

  useEffect(() => {
    loadAfterOwnerChange(browserStorage(), 'hyperliquid', previousOwners.current.hyperliquid, hyperliquidOwner)
    previousOwners.current.hyperliquid = hyperliquidOwner
  }, [hyperliquidOwner])

  return (
    <TradingAgentSession
      pacificaOwner={pacificaOwner}
      hyperliquidOwner={hyperliquidOwner}
      chainId={chainId}
      solanaSignMessage={solana.signMessage}
      signTypedData={signTypedDataAsync}
    >
      {children}
    </TradingAgentSession>
  )
}

function TradingAgentSession({
  children,
  pacificaOwner,
  hyperliquidOwner,
  chainId,
  solanaSignMessage,
  signTypedData,
}: {
  children: ReactNode
  pacificaOwner: string | null
  hyperliquidOwner: string | null
  chainId: number
  solanaSignMessage?: (message: Uint8Array) => Promise<Uint8Array>
  signTypedData: ReturnType<typeof useSignTypedData>['signTypedDataAsync']
}) {
  const [pacifica, setPacifica] = useState(() => initialState('pacifica', pacificaOwner))
  const [hyperliquid, setHyperliquid] = useState(() => initialState('hyperliquid', hyperliquidOwner))
  const owners = useRef({ pacifica: pacificaOwner, hyperliquid: hyperliquidOwner })
  owners.current = { pacifica: pacificaOwner, hyperliquid: hyperliquidOwner }
  if (pacifica.ownerAddress !== pacificaOwner) {
    setPacifica(initialState('pacifica', pacificaOwner))
  }
  if (hyperliquid.ownerAddress?.toLowerCase() !== hyperliquidOwner?.toLowerCase()) {
    setHyperliquid(initialState('hyperliquid', hyperliquidOwner))
  }

  const authorize = async (venue: Venue) => {
    const setState = venue === 'pacifica' ? setPacifica : setHyperliquid
    const ownerAddress = venue === 'pacifica' ? pacificaOwner : hyperliquidOwner
    if (!ownerAddress) throw new Error(`Connect the ${venue} owner wallet first`)
    setState({ venue, ownerAddress, agentAddress: null, status: 'authorizing', error: null })
    try {
      const agent = venue === 'pacifica'
        ? await authorizePacifica(ownerAddress)
        : await authorizeHyperliquid(ownerAddress)
      if (!ownerStillCurrent(venue, ownerAddress, owners.current)) {
        clearStoredTradingAgent(browserStorage(), venue, ownerAddress)
        throw new Error(`${venue} owner changed during agent authorization`)
      }
      setState({ venue, ownerAddress, agentAddress: agent.agentAddress, status: 'ready', error: null })
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Agent authorization failed'
      if (ownerStillCurrent(venue, ownerAddress, owners.current)) {
        setState({ venue, ownerAddress, agentAddress: null, status: 'error', error: message })
      }
      throw error
    }
  }

  const authorizePacifica = (ownerAddress: string) => {
    if (!solanaSignMessage) throw new Error('Solana wallet does not support message signing')
    return authorizePacificaAgent({
      storage: browserStorage(),
      ownerAddress,
      signMessage: solanaSignMessage,
      relay: (request) => relayAuthorization('/api/v1/live/agents/pacifica/bind', request),
    })
  }

  const authorizeHyperliquid = (ownerAddress: string) => authorizeHyperliquidAgent({
    storage: browserStorage(),
    ownerAddress,
    chainId,
    signTypedData,
    relay: (request) => relayAuthorization('/api/v1/live/agents/hyperliquid/approve', request),
  })

  const sign = async (request: SigningRequest) => {
    const currentOwner = request.venue === 'pacifica' ? pacificaOwner : hyperliquidOwner
    const matches = request.venue === 'hyperliquid'
      ? currentOwner?.toLowerCase() === request.account.toLowerCase()
      : currentOwner === request.account
    if (!matches) throw new Error(`${request.venue} owner changed during execution`)
    return signWithStoredTradingAgent(browserStorage(), request)
  }

  const clear = (venue: Venue) => {
    const ownerAddress = venue === 'pacifica' ? pacificaOwner : hyperliquidOwner
    if (ownerAddress) clearStoredTradingAgent(browserStorage(), venue, ownerAddress)
    const setState = venue === 'pacifica' ? setPacifica : setHyperliquid
    setState(missingState(venue, ownerAddress))
  }

  return (
    <TradingAgentContext.Provider value={{ pacifica, hyperliquid, authorize, sign, clear }}>
      {children}
    </TradingAgentContext.Provider>
  )
}

function ownerStillCurrent(
  venue: Venue,
  expectedOwner: string,
  owners: { pacifica: string | null; hyperliquid: string | null },
): boolean {
  const current = venue === 'pacifica' ? owners.pacifica : owners.hyperliquid
  return venue === 'hyperliquid'
    ? current?.toLowerCase() === expectedOwner.toLowerCase()
    : current === expectedOwner
}

function initialState(venue: Venue, ownerAddress: string | null): TradingAgentState {
  const agent = ownerAddress
    ? loadAfterOwnerChange(browserStorage(), venue, null, ownerAddress)
    : null
  return agent
    ? { venue, ownerAddress, agentAddress: agent.agentAddress, status: 'ready', error: null }
    : missingState(venue, ownerAddress)
}

async function relayAuthorization(
  path: string,
  request: PacificaBindAgentRequest | HyperliquidApproveAgentRequest,
): Promise<void> {
  const response = await apiFetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  if (response.ok) return
  const body = await response.json().catch(() => null) as { error?: string } | null
  throw new Error(body?.error ?? `Agent authorization failed (${response.status})`)
}
