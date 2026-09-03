import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useAccount, useSignTypedData, useSwitchChain } from 'wagmi'
import { mainnet } from 'wagmi/chains'

import { apiError, apiFetch } from '@/lib/api'
import type { SigningRequest } from '@/types/signing'
import {
  approveHyperliquidBuilderFee,
  authorizeHyperliquidAgent,
  hasApprovedHyperliquidBuilderFee,
  hyperliquidBuilderAddress,
  type HyperliquidApproveAgentRequest,
  type HyperliquidApproveBuilderFeeRequest,
} from './hyperliquid-agent.ts'
import {
  authorizePacificaAgent,
  type PacificaApproveBuilderCodeRequest,
  type PacificaBindAgentRequest,
} from './pacifica-agent.ts'
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
  if (typeof window === 'undefined') throw new Error('Trading authorization requires a browser session')
  return window.sessionStorage
}

export function TradingAgentProvider({ children }: { children: ReactNode }) {
  const solana = useWallet()
  const evm = useAccount()
  const { signTypedDataAsync } = useSignTypedData()
  const { switchChainAsync } = useSwitchChain()
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
      chainId={evm.chainId}
      switchToAuthorizationChain={() => switchChainAsync({ chainId: mainnet.id })}
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
  switchToAuthorizationChain,
  solanaSignMessage,
  signTypedData,
}: {
  children: ReactNode
  pacificaOwner: string | null
  hyperliquidOwner: string | null
  chainId?: number
  switchToAuthorizationChain: () => Promise<unknown>
  solanaSignMessage?: (message: Uint8Array) => Promise<Uint8Array>
  signTypedData: ReturnType<typeof useSignTypedData>['signTypedDataAsync']
}) {
  const [pacifica, setPacifica] = useState(() => initialState('pacifica', pacificaOwner))
  const [hyperliquid, setHyperliquid] = useState(() => initialState('hyperliquid', hyperliquidOwner))
  const owners = useRef({ pacifica: pacificaOwner, hyperliquid: hyperliquidOwner })
  const builderApproval = useRef<{ ownerAddress: string; promise: Promise<void> } | null>(null)
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
      const message = error instanceof Error ? error.message : 'Authorization failed'
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
      relayBuilderApproval: (request) => relayAuthorization('/api/v1/live/agents/pacifica/approve-builder-code', request),
    })
  }

  const authorizeHyperliquid = async (ownerAddress: string) => {
    if (chainId !== mainnet.id) await switchToAuthorizationChain()
    return authorizeHyperliquidAgent({
      storage: browserStorage(),
      ownerAddress,
      chainId: mainnet.id,
      builderFeeApproved: hasApprovedHyperliquidBuilderFee,
      signTypedData: (typedData) => signTypedData(typedData),
      signBuilderTypedData: (typedData) => signTypedData(typedData),
      relay: (request) => relayAuthorization('/api/v1/live/agents/hyperliquid/approve', request),
      relayBuilderApproval: (request) => relayAuthorization('/api/v1/live/agents/hyperliquid/approve-builder-fee', request),
    })
  }

  const ensureHyperliquidBuilderFee = async (ownerAddress: string) => {
    const normalizedOwner = ownerAddress.toLowerCase()
    if (builderApproval.current) {
      if (builderApproval.current.ownerAddress !== normalizedOwner) {
        throw new Error('Hyperliquid owner changed during builder approval')
      }
      return builderApproval.current.promise
    }
    const approval = (async () => {
      if (!ownerStillCurrent('hyperliquid', ownerAddress, owners.current)) {
        throw new Error('Hyperliquid owner changed during builder approval')
      }
      if (await hasApprovedHyperliquidBuilderFee(ownerAddress, hyperliquidBuilderAddress)) return
      if (!ownerStillCurrent('hyperliquid', ownerAddress, owners.current)) {
        throw new Error('Hyperliquid owner changed during builder approval')
      }
      if (chainId !== mainnet.id) await switchToAuthorizationChain()
      if (!ownerStillCurrent('hyperliquid', ownerAddress, owners.current)) {
        throw new Error('Hyperliquid owner changed during builder approval')
      }
      await approveHyperliquidBuilderFee({
        ownerAddress,
        builderAddress: hyperliquidBuilderAddress,
        chainId: mainnet.id,
        signTypedData: (typedData) => signTypedData(typedData),
        relay: (request) => relayAuthorization('/api/v1/live/agents/hyperliquid/approve-builder-fee', request),
      })
    })()
    builderApproval.current = { ownerAddress: normalizedOwner, promise: approval }
    try {
      await approval
    } finally {
      builderApproval.current = null
    }
  }

  const sign = async (request: SigningRequest) => {
    const currentOwner = request.venue === 'pacifica' ? pacificaOwner : hyperliquidOwner
    const matches = request.venue === 'hyperliquid'
      ? currentOwner?.toLowerCase() === request.account.toLowerCase()
      : currentOwner === request.account
    if (!matches) throw new Error(`${request.venue} owner changed during execution`)
    if (request.venue === 'hyperliquid' && (request.action === 'open' || request.action === 'close')) {
      await ensureHyperliquidBuilderFee(request.account)
    }
    if (!ownerStillCurrent(request.venue, request.account, owners.current)) {
      throw new Error(`${request.venue} owner changed during execution`)
    }
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
  let agent = ownerAddress
    ? loadAfterOwnerChange(browserStorage(), venue, null, ownerAddress)
    : null
  if (
    agent?.venue === 'hyperliquid' &&
    agent.builderAddress?.toLowerCase() !== hyperliquidBuilderAddress.toLowerCase()
  ) {
    clearStoredTradingAgent(browserStorage(), venue, agent.ownerAddress)
    agent = null
  }
  return agent
    ? { venue, ownerAddress, agentAddress: agent.agentAddress, status: 'ready', error: null }
    : missingState(venue, ownerAddress)
}

async function relayAuthorization(
  path: string,
  request: PacificaBindAgentRequest | PacificaApproveBuilderCodeRequest | HyperliquidApproveAgentRequest | HyperliquidApproveBuilderFeeRequest,
): Promise<void> {
  const response = await apiFetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  if (response.ok) return
  const body = await response.json().catch(() => null) as { error?: string } | null
  throw apiError(response.status, 'Unable to authorize the trading agent. Please try again.', body)
}
