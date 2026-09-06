import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useAccount, useSignTypedData, useSwitchChain } from 'wagmi'
import { bsc, mainnet } from 'wagmi/chains'

import { apiError, apiFetch } from '@/lib/api'
import type { SigningRequest } from '@/types/signing'
import {
  authorizeAsterAgent,
  type AsterApproveAgentRequest,
} from './aster-agent.ts'
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
import { assertSupportedSigningVenue, signWithStoredTradingAgent } from './signing.ts'
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
  const asterOwner = hyperliquidOwner
  const previousOwners = useRef<{ pacifica: string | null; hyperliquid: string | null; aster: string | null }>({
    pacifica: null,
    hyperliquid: null,
    aster: null,
  })
  useEffect(() => {
    loadAfterOwnerChange(browserStorage(), 'pacifica', previousOwners.current.pacifica, pacificaOwner)
    previousOwners.current.pacifica = pacificaOwner
  }, [pacificaOwner])

  useEffect(() => {
    loadAfterOwnerChange(browserStorage(), 'hyperliquid', previousOwners.current.hyperliquid, hyperliquidOwner)
    previousOwners.current.hyperliquid = hyperliquidOwner
  }, [hyperliquidOwner])

  useEffect(() => {
    loadAfterOwnerChange(browserStorage(), 'aster', previousOwners.current.aster, asterOwner)
    previousOwners.current.aster = asterOwner
  }, [asterOwner])

  return (
    <TradingAgentSession
      pacificaOwner={pacificaOwner}
      hyperliquidOwner={hyperliquidOwner}
      asterOwner={asterOwner}
      chainId={evm.chainId}
      switchToAuthorizationChain={(targetChainId) => switchChainAsync({ chainId: targetChainId })}
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
  asterOwner,
  chainId,
  switchToAuthorizationChain,
  solanaSignMessage,
  signTypedData,
}: {
  children: ReactNode
  pacificaOwner: string | null
  hyperliquidOwner: string | null
  asterOwner: string | null
  chainId?: number
  switchToAuthorizationChain: (chainId: typeof mainnet.id | typeof bsc.id) => Promise<unknown>
  solanaSignMessage?: (message: Uint8Array) => Promise<Uint8Array>
  signTypedData: ReturnType<typeof useSignTypedData>['signTypedDataAsync']
}) {
  const [pacifica, setPacifica] = useState(() => initialState('pacifica', pacificaOwner))
  const [hyperliquid, setHyperliquid] = useState(() => initialState('hyperliquid', hyperliquidOwner))
  const [aster, setAster] = useState(() => initialState('aster', asterOwner))
  const owners = useRef({ pacifica: pacificaOwner, hyperliquid: hyperliquidOwner, aster: asterOwner })
  const builderApproval = useRef<{ ownerAddress: string; promise: Promise<void> } | null>(null)
  owners.current = { pacifica: pacificaOwner, hyperliquid: hyperliquidOwner, aster: asterOwner }
  if (pacifica.ownerAddress !== pacificaOwner) {
    setPacifica(initialState('pacifica', pacificaOwner))
  }
  if (hyperliquid.ownerAddress?.toLowerCase() !== hyperliquidOwner?.toLowerCase()) {
    setHyperliquid(initialState('hyperliquid', hyperliquidOwner))
  }
  if (aster.ownerAddress?.toLowerCase() !== asterOwner?.toLowerCase()) {
    setAster(initialState('aster', asterOwner))
  }

  const authorize = async (venue: Venue) => {
    const setState = venue === 'pacifica' ? setPacifica : venue === 'aster' ? setAster : setHyperliquid
    const ownerAddress = venue === 'pacifica' ? pacificaOwner : venue === 'aster' ? asterOwner : hyperliquidOwner
    if (!ownerAddress) throw new Error(`Connect the ${venue} owner wallet first`)
    setState({ venue, ownerAddress, agentAddress: null, status: 'authorizing', error: null })
    try {
      const agent = venue === 'pacifica'
        ? await authorizePacifica(ownerAddress)
        : venue === 'aster'
          ? await authorizeAster(ownerAddress)
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
      builderCodeApproved: hasApprovedPacificaBuilderCode,
      relay: (request) => relayAuthorization('/api/v1/live/agents/pacifica/bind', request),
      relayBuilderApproval: (request) => relayAuthorization('/api/v1/live/agents/pacifica/approve-builder-code', request),
    })
  }

  const authorizeHyperliquid = async (ownerAddress: string) => {
    if (chainId !== mainnet.id) await switchToAuthorizationChain(mainnet.id)
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

  const authorizeAster = async (ownerAddress: string) => {
    if (chainId !== bsc.id) await switchToAuthorizationChain(bsc.id)
    if (!ownerStillCurrent('aster', ownerAddress, owners.current)) {
      throw new Error('Aster owner changed during agent authorization')
    }
    return authorizeAsterAgent({
      storage: browserStorage(),
      ownerAddress,
      signTypedData: (typedData) => signTypedData(typedData),
      relay: (request) => relayAuthorization('/api/v1/live/agents/aster/approve', request),
      ownerStillCurrent: () => ownerStillCurrent('aster', ownerAddress, owners.current),
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
      if (chainId !== mainnet.id) await switchToAuthorizationChain(mainnet.id)
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
    assertSupportedSigningVenue(request.venue)
    const currentOwner = request.venue === 'pacifica'
      ? pacificaOwner
      : request.venue === 'aster' ? asterOwner : hyperliquidOwner
    const matches = request.venue === 'pacifica'
      ? currentOwner === request.account
      : currentOwner?.toLowerCase() === request.account.toLowerCase()
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
    const ownerAddress = venue === 'pacifica' ? pacificaOwner : venue === 'aster' ? asterOwner : hyperliquidOwner
    if (ownerAddress) clearStoredTradingAgent(browserStorage(), venue, ownerAddress)
    const setState = venue === 'pacifica' ? setPacifica : venue === 'aster' ? setAster : setHyperliquid
    setState(missingState(venue, ownerAddress))
  }

  return (
    <TradingAgentContext.Provider value={{ pacifica, hyperliquid, aster, authorize, sign, clear }}>
      {children}
    </TradingAgentContext.Provider>
  )
}

function ownerStillCurrent(
  venue: Venue,
  expectedOwner: string,
  owners: { pacifica: string | null; hyperliquid: string | null; aster: string | null },
): boolean {
  const current = venue === 'pacifica' ? owners.pacifica : venue === 'aster' ? owners.aster : owners.hyperliquid
  return venue === 'pacifica'
    ? current === expectedOwner
    : current?.toLowerCase() === expectedOwner.toLowerCase()
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
  request: AsterApproveAgentRequest | PacificaBindAgentRequest | PacificaApproveBuilderCodeRequest | HyperliquidApproveAgentRequest | HyperliquidApproveBuilderFeeRequest,
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

async function hasApprovedPacificaBuilderCode(ownerAddress: string): Promise<boolean> {
  const response = await apiFetch(`/api/v1/live/agents/pacifica/builder-code-approval?account=${encodeURIComponent(ownerAddress)}`)
  if (!response.ok) throw apiError(response.status, 'Unable to verify Pacifica builder approval. Please try again.')
  const body = await response.json() as { approved?: unknown }
  if (typeof body.approved !== 'boolean') throw new Error('Pacifica returned an invalid builder approval status')
  return body.approved
}
