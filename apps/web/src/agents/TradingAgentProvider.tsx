import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import type { Hex } from 'viem'
import { useAccount, useDisconnect, useSignTypedData, useSwitchChain } from 'wagmi'
import { bsc, mainnet } from 'wagmi/chains'

import { apiError, apiFetch, userErrorMessage } from '@/lib/api'
import type { SigningRequest } from '@/types/signing'
import {
  authorizeAsterDataAgent,
  prepareAsterDataAgentAuthorization,
  type AsterDataAgentApprovalTypedData,
} from './aster-data-agent.ts'
import {
  asterBuilderAddress,
  authorizeAsterAgent,
  type AsterApprovalTypedData,
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
  revokePacificaAgent,
  type PacificaApproveBuilderCodeRequest,
  type PacificaBindAgentRequest,
  type PacificaRevokeAgentRequest,
} from './pacifica-agent.ts'
import { assertSupportedSigningVenue, signWithStoredTradingAgent } from './signing.ts'
import {
  createTradingAgentStore,
  type TradingAgentStore,
} from './storage.ts'
import type { TradingAgentState, Venue, WalletKind } from './types'
import { TradingAgentContext } from './TradingAgentContext'

function missingState(venue: Venue, ownerAddress: string | null): TradingAgentState {
  return { venue, ownerAddress, agentAddress: null, status: 'missing', error: null }
}

let persistentStore: TradingAgentStore | null = null
const volatilePendingPacificaRevocations = new Map<string, string>()

function browserStorage(): TradingAgentStore {
  if (typeof window === 'undefined') throw new Error('Trading authorization requires a browser session')
  if (!persistentStore) {
    try {
      clearLegacyAgentStorage(window.sessionStorage)
    } catch {
      // The encrypted vault can still work when legacy Web Storage is unavailable.
    }
    persistentStore = createTradingAgentStore(window.indexedDB, window.crypto)
  }
  return persistentStore
}

function clearLegacyAgentStorage(storage: Storage): void {
  for (let index = storage.length - 1; index >= 0; index -= 1) {
    const key = storage.key(index)
    if (key?.startsWith('orbital.agent.') && key.includes('.v1:')) storage.removeItem(key)
  }
}

export function TradingAgentProvider({ children }: { children: ReactNode }) {
  const solana = useWallet()
  const evm = useAccount()
  const { signTypedDataAsync } = useSignTypedData()
  const { switchChainAsync } = useSwitchChain()
  const { disconnectAsync: disconnectEvm } = useDisconnect()
  const pacificaOwner = solana.connected && solana.publicKey ? solana.publicKey.toBase58() : null
  const hyperliquidOwner = evm.isConnected && evm.address ? evm.address : null
  const asterOwner = hyperliquidOwner
  const storage = browserStorage()

  return (
    <TradingAgentSession
      storage={storage}
      pacificaOwner={pacificaOwner}
      hyperliquidOwner={hyperliquidOwner}
      asterOwner={asterOwner}
      chainId={evm.chainId}
      switchToAuthorizationChain={(targetChainId) => switchChainAsync({ chainId: targetChainId })}
      solanaSignMessage={solana.signMessage}
      signTypedData={signTypedDataAsync}
      signAsterTypedData={(typedData) => typedData.primaryType === 'ApproveBuilder'
        ? signTypedDataAsync(typedData)
        : signTypedDataAsync(typedData)}
      disconnectSolana={() => solana.disconnect()}
      disconnectEvm={disconnectEvm}
    >
      {children}
    </TradingAgentSession>
  )
}

function TradingAgentSession({
  children,
  storage,
  pacificaOwner,
  hyperliquidOwner,
  asterOwner,
  chainId,
  switchToAuthorizationChain,
  solanaSignMessage,
  signTypedData,
  signAsterTypedData,
  disconnectSolana,
  disconnectEvm,
}: {
  children: ReactNode
  storage: TradingAgentStore
  pacificaOwner: string | null
  hyperliquidOwner: string | null
  asterOwner: string | null
  chainId?: number
  switchToAuthorizationChain: (chainId: typeof mainnet.id | typeof bsc.id) => Promise<unknown>
  solanaSignMessage?: (message: Uint8Array) => Promise<Uint8Array>
  signTypedData: ReturnType<typeof useSignTypedData>['signTypedDataAsync']
  signAsterTypedData: (typedData: AsterApprovalTypedData | AsterDataAgentApprovalTypedData) => Promise<Hex>
  disconnectSolana: () => Promise<void>
  disconnectEvm: () => Promise<void>
}) {
  const [pacifica, setPacifica] = useState(() => initialState('pacifica', pacificaOwner))
  const [hyperliquid, setHyperliquid] = useState(() => initialState('hyperliquid', hyperliquidOwner))
  const [aster, setAster] = useState(() => initialState('aster', asterOwner))
  const owners = useRef({ pacifica: pacificaOwner, hyperliquid: hyperliquidOwner, aster: asterOwner })
  const builderApproval = useRef<{ ownerAddress: string; promise: Promise<void> } | null>(null)
  const revokedPacificaAgents = useRef(new Set<string>())
  owners.current = { pacifica: pacificaOwner, hyperliquid: hyperliquidOwner, aster: asterOwner }

  useEffect(() => {
    let active = true
    const restore = async (
      venue: Venue,
      ownerAddress: string | null,
      setState: (state: TradingAgentState) => void,
    ) => {
      await Promise.resolve()
      if (!active) return
      setState(initialState(venue, ownerAddress))
      if (!ownerAddress) return
      try {
        let agent = await storage.restore(venue, ownerAddress)
        const expectedBuilder = venue === 'hyperliquid'
          ? hyperliquidBuilderAddress
          : venue === 'aster' ? asterBuilderAddress : null
        if (expectedBuilder && agent &&
          agent.builderAddress?.toLowerCase() !== expectedBuilder.toLowerCase()) {
          await storage.clear(venue, ownerAddress)
          agent = null
        }
        if (!active) return
        setState(agent
          ? { venue, ownerAddress, agentAddress: agent.agentAddress, status: 'ready', error: null }
          : missingState(venue, ownerAddress))
      } catch (error) {
        if (!active) return
        setState({
          venue,
          ownerAddress,
          agentAddress: null,
          status: 'error',
          error: error instanceof Error ? error.message : 'Unable to restore authorization',
        })
      }
    }
    void restore('pacifica', pacificaOwner, setPacifica)
    void restore('hyperliquid', hyperliquidOwner, setHyperliquid)
    void restore('aster', asterOwner, setAster)
    return () => { active = false }
  }, [asterOwner, hyperliquidOwner, pacificaOwner, storage])

  const authorize = async (venue: Venue) => {
    const setState = venue === 'pacifica' ? setPacifica : venue === 'aster' ? setAster : setHyperliquid
    const ownerAddress = venue === 'pacifica' ? pacificaOwner : venue === 'aster' ? asterOwner : hyperliquidOwner
    if (!ownerAddress) throw new Error(`Connect the ${venue} owner wallet first`)
    setState({ venue, ownerAddress, agentAddress: null, status: 'authorizing', error: null })
    try {
      const agent = await withAgentLock(venue, ownerAddress, async () => {
        await storage.ready()
        return venue === 'pacifica'
          ? reauthorizePacifica(ownerAddress)
          : venue === 'aster'
            ? authorizeAster(ownerAddress)
            : authorizeHyperliquid(ownerAddress)
      })
      if (!ownerStillCurrent(venue, ownerAddress, owners.current)) {
        throw new Error(`${venue} owner changed during agent authorization`)
      }
      setState({ venue, ownerAddress, agentAddress: agent.agentAddress, status: 'ready', error: null })
    } catch (error) {
      const message = userErrorMessage(error, 'Authorization failed')
      if (ownerStillCurrent(venue, ownerAddress, owners.current)) {
        setState({ venue, ownerAddress, agentAddress: null, status: 'error', error: message })
      }
      throw error
    }
  }

  const authorizePacifica = (ownerAddress: string) => {
    if (!solanaSignMessage) throw new Error('Solana wallet does not support message signing')
    return authorizePacificaAgent({
      storage,
      ownerAddress,
      signMessage: solanaSignMessage,
      builderCodeApproved: hasApprovedPacificaBuilderCode,
      relay: (request) => relayAuthorization('/api/v1/live/agents/pacifica/bind', request),
      relayRevocation: (request) => relayAuthorization('/api/v1/live/agents/pacifica/revoke', request),
      relayBuilderApproval: (request) => relayAuthorization('/api/v1/live/agents/pacifica/approve-builder-code', request),
      rememberPendingRevocation: (agentAddress) => rememberPendingPacificaRevocation(ownerAddress, agentAddress),
      forgetPendingRevocation: (agentAddress) => forgetPendingPacificaRevocation(ownerAddress, agentAddress),
    })
  }

  const revokePacifica = async (ownerAddress: string) => {
    const agent = await storage.restore('pacifica', ownerAddress)
    const pendingAgentAddress = pendingPacificaRevocation(ownerAddress)
    const agentAddresses = new Set([agent?.agentAddress, pendingAgentAddress].filter((value): value is string => !!value))
    for (const agentAddress of agentAddresses) {
      await revokePacificaAddress(ownerAddress, agentAddress)
      if (agentAddress === pendingAgentAddress) forgetPendingPacificaRevocation(ownerAddress, agentAddress)
    }
  }

  const revokePacificaAddress = async (ownerAddress: string, agentAddress: string) => {
    const revocationKey = `${ownerAddress}:${agentAddress}`
    if (revokedPacificaAgents.current.has(revocationKey)) return
    if (!solanaSignMessage) throw new Error('Solana wallet does not support message signing')
    await revokePacificaAgent({
      ownerAddress,
      agentAddress,
      signMessage: async (message) => {
        if (!ownerStillCurrent('pacifica', ownerAddress, owners.current)) {
          throw new Error('Pacifica owner changed during agent revocation')
        }
        const signature = await solanaSignMessage(message)
        if (!ownerStillCurrent('pacifica', ownerAddress, owners.current)) {
          throw new Error('Pacifica owner changed during agent revocation')
        }
        return signature
      },
      relay: (request) => {
        if (!ownerStillCurrent('pacifica', ownerAddress, owners.current)) {
          throw new Error('Pacifica owner changed during agent revocation')
        }
        return relayAuthorization('/api/v1/live/agents/pacifica/revoke', request)
      },
    })
    revokedPacificaAgents.current.add(revocationKey)
  }

  const reauthorizePacifica = async (ownerAddress: string) => {
    const agent = await storage.restore('pacifica', ownerAddress)
    await revokePacifica(ownerAddress)
    await storage.clear('pacifica', ownerAddress)
    if (agent) revokedPacificaAgents.current.delete(`${ownerAddress}:${agent.agentAddress}`)
    return authorizePacifica(ownerAddress)
  }

  const authorizeHyperliquid = async (ownerAddress: string) => {
    if (chainId !== mainnet.id) await switchToAuthorizationChain(mainnet.id)
    return authorizeHyperliquidAgent({
      storage,
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
    const isCurrent = () => ownerStillCurrent('aster', ownerAddress, owners.current)
    return authorizeAsterAgent({
      storage,
      ownerAddress,
      signTypedData: signAsterTypedData,
      relay: (request) => relayAuthorization('/api/v1/live/agents/aster/approve', request),
      prepareReadOnly: (executionAgent) => prepareAsterDataAgentAuthorization({
        account: ownerAddress, executionAgent, isCurrent,
      }),
      authorizeReadOnly: (preparation, signature, executionAgent) => authorizeAsterDataAgent({
        account: ownerAddress, executionAgent, preparation, signature, isCurrent,
      }),
      onSignatureStep: (authorizationStep) => setAster((current) => ({ ...current, authorizationStep })),
      ownerStillCurrent: isCurrent,
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
    const signed = await signWithStoredTradingAgent(storage, request)
    if (!ownerStillCurrent(request.venue, request.account, owners.current)) {
      throw new Error(`${request.venue} owner changed during signing`)
    }
    return signed
  }

  const disconnectWallet = async (wallet: WalletKind) => {
    if (wallet === 'solana') {
      const ownerAddress = owners.current.pacifica
      if (ownerAddress) {
        setPacifica((current) => ({ ...current, status: 'disconnecting', error: null }))
        try {
          await withAgentLock('pacifica', ownerAddress, async () => {
            const agent = await storage.restore('pacifica', ownerAddress)
            await revokePacifica(ownerAddress)
            await storage.clear('pacifica', ownerAddress)
            if (agent) revokedPacificaAgents.current.delete(`${ownerAddress}:${agent.agentAddress}`)
          })
        } catch (error) {
          setPacifica((current) => ({
            ...current,
            status: 'error',
            error: error instanceof Error ? error.message : 'Unable to disconnect Pacifica',
          }))
          throw error
        }
        if (!ownerStillCurrent('pacifica', ownerAddress, owners.current)) {
          throw new Error('Pacifica owner changed during disconnect')
        }
        setPacifica(missingState('pacifica', ownerAddress))
      }
      try {
        await disconnectSolana()
      } catch (error) {
        const detail = error instanceof Error && error.message ? ` (${error.message})` : ''
        setPacifica({
          ...missingState('pacifica', ownerAddress),
          status: 'error',
          error: `Pacifica authorization was removed, but the Solana wallet did not disconnect${detail}`,
        })
        throw error
      }
      return
    }

    const ownerAddress = owners.current.hyperliquid
    if (ownerAddress) {
      setHyperliquid((current) => ({ ...current, status: 'disconnecting', error: null }))
      setAster((current) => ({ ...current, status: 'disconnecting', error: null }))
      try {
        await withAgentLock('aster', ownerAddress, () => withAgentLock('hyperliquid', ownerAddress, async () => {
          await storage.clear('hyperliquid', ownerAddress)
          setHyperliquid(missingState('hyperliquid', ownerAddress))
          await storage.clear('aster', ownerAddress)
          setAster(missingState('aster', ownerAddress))
        }))
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unable to disconnect EVM wallet'
        setHyperliquid((current) => current.status === 'disconnecting' ? { ...current, status: 'error', error: message } : current)
        setAster((current) => current.status === 'disconnecting' ? { ...current, status: 'error', error: message } : current)
        throw error
      }
      if (!ownerStillCurrent('hyperliquid', ownerAddress, owners.current)) {
        throw new Error('EVM owner changed during disconnect')
      }
    }
    try {
      await disconnectEvm()
    } catch (error) {
      const detail = error instanceof Error && error.message ? ` (${error.message})` : ''
      const message = `Local EVM authorizations were removed, but the wallet did not disconnect${detail}`
      setHyperliquid({ ...missingState('hyperliquid', ownerAddress), status: 'error', error: message })
      setAster({ ...missingState('aster', ownerAddress), status: 'error', error: message })
      throw error
    }
  }

  return (
    <TradingAgentContext.Provider value={{
      pacifica, hyperliquid, aster, authorize, sign, disconnectWallet,
    }}>
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
  return ownerAddress
    ? { venue, ownerAddress, agentAddress: null, status: 'restoring', error: null }
    : missingState(venue, ownerAddress)
}

function withAgentLock<T>(venue: Venue, ownerAddress: string, task: () => Promise<T>): Promise<T> {
  const locks = typeof navigator === 'undefined' ? undefined : navigator.locks
  if (!locks) return task()
  const normalizedOwner = venue === 'pacifica' ? ownerAddress : ownerAddress.toLowerCase()
  return locks.request(`orbital-agent:${venue}:${normalizedOwner}`, task)
}

function pendingPacificaRevocation(ownerAddress: string): string | null {
  try {
    return window.localStorage.getItem(pendingPacificaRevocationKey(ownerAddress))
      ?? volatilePendingPacificaRevocations.get(ownerAddress) ?? null
  } catch {
    return volatilePendingPacificaRevocations.get(ownerAddress) ?? null
  }
}

function rememberPendingPacificaRevocation(ownerAddress: string, agentAddress: string): void {
  volatilePendingPacificaRevocations.set(ownerAddress, agentAddress)
  try {
    window.localStorage.setItem(pendingPacificaRevocationKey(ownerAddress), agentAddress)
  } catch {
    // The compensating revoke still runs immediately when localStorage is unavailable.
  }
}

function forgetPendingPacificaRevocation(ownerAddress: string, agentAddress: string): void {
  if (volatilePendingPacificaRevocations.get(ownerAddress) === agentAddress) {
    volatilePendingPacificaRevocations.delete(ownerAddress)
  }
  try {
    const key = pendingPacificaRevocationKey(ownerAddress)
    if (window.localStorage.getItem(key) === agentAddress) window.localStorage.removeItem(key)
  } catch {
    // A stale public address is harmless and can be retried later.
  }
}

function pendingPacificaRevocationKey(ownerAddress: string): string {
  return `orbital.agent.pacifica.pending-revoke:${ownerAddress}`
}

async function relayAuthorization(
  path: string,
  request: AsterApproveAgentRequest | PacificaBindAgentRequest | PacificaApproveBuilderCodeRequest | PacificaRevokeAgentRequest | HyperliquidApproveAgentRequest | HyperliquidApproveBuilderFeeRequest,
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
