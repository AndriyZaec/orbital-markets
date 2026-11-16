import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import type { Hex } from 'viem'
import { useAccount, useConnections, useDisconnect, useSignTypedData, useSwitchChain } from 'wagmi'
import { bsc, mainnet } from 'wagmi/chains'

import { apiError, apiFetch, userErrorMessage } from '@/lib/api'
import { useVenueAccounts } from '@/accounts/useVenueAccounts'
import type { SigningRequest } from '@/types/signing'
import type { AsterDataAgentApprovalTypedData } from './aster-data-agent.ts'
import type { AsterApprovalTypedData, AsterApproveAgentRequest } from './aster-agent.ts'
import type {
  HyperliquidApproveAgentRequest,
  HyperliquidApproveBuilderFeeRequest,
} from './hyperliquid-agent.ts'
import type {
  PacificaApproveBuilderCodeRequest,
  PacificaBindAgentRequest,
  PacificaRevokeAgentRequest,
} from './pacifica-agent.ts'
import { asterBuilderAddress, hyperliquidBuilderAddress } from './builder-config.ts'
import { fetchAgentApprovalName } from './approval-config.ts'
import type { TradingAgentStore } from './storage.ts'
import { browserStorage } from './browser-storage'
import type { TradingAgentState, Venue } from './types'
import { TradingAgentContext } from './TradingAgentContext'
import type { OwnerTransactionSigner } from './phoenix-message.ts'

function missingState(venue: Venue, ownerAddress: string | null): TradingAgentState {
  return { venue, ownerAddress, agentAddress: null, status: 'missing', error: null }
}

// Each venue/owner restores independently. Connecting another wallet must not
// reset an already authorized account or repeat its reconciliation requests.
function restoreTradingAgent(venue: Venue, ownerAddress: string | null, storage: TradingAgentStore,
  setState: (state: TradingAgentState) => void, isCurrent: () => boolean) {
  let active = true
  const restore = async () => {
    await Promise.resolve()
    if (!active || !isCurrent()) return
    setState(initialState(venue, ownerAddress))
    if (!ownerAddress) return
    try {
      let agent = venue === 'phoenix'
        ? await withAgentLock('phoenix', ownerAddress, async () => {
          const { restorePhoenixAgent, createPhoenixTransport } = await import('./phoenix-agent.ts')
          return restorePhoenixAgent({ storage, ownerAddress, transport: createPhoenixTransport(), isCurrent: () => active && isCurrent() })
        })
        : venue === 'aster'
        ? await withAgentLock('aster', ownerAddress, async () => {
          const { reconcilePendingAsterAgent } = await import('./aster-agent.ts')
          return await reconcilePendingAsterAgent({ storage, ownerAddress,
            reconcile: (candidates) => reconcileAsterAuthorization(ownerAddress, candidates),
          }) ?? await storage.restore('aster', ownerAddress)
        })
        : await storage.restore(venue, ownerAddress)
      const expectedBuilder = venue === 'hyperliquid' ? hyperliquidBuilderAddress : venue === 'aster' ? asterBuilderAddress : null
      if (expectedBuilder && agent && agent.builderAddress?.toLowerCase() !== expectedBuilder.toLowerCase()) {
        await storage.clear(venue, ownerAddress)
        agent = null
      }
      if (!active || !isCurrent()) return
      setState(agent ? { venue, ownerAddress, agentAddress: agent.agentAddress, status: 'ready', error: null }
        : missingState(venue, ownerAddress))
    } catch (error) {
      if (!active || !isCurrent()) return
      setState({ venue, ownerAddress, agentAddress: null,
        status: venue === 'phoenix' && error instanceof Error && error.name === 'PhoenixPendingError' ? 'pending' : 'error',
        error: error instanceof Error ? error.message : 'Unable to restore authorization',
      })
    }
  }
  void restore()
  return () => { active = false }
}

const volatilePendingPacificaRevocations = new Map<string, string>()

export function TradingAgentProvider({ children }: { children: ReactNode }) {
  const solana = useWallet()
  const evm = useAccount()
  const { signTypedDataAsync } = useSignTypedData()
  const { switchChainAsync } = useSwitchChain()
  const evmConnections = useConnections()
  const { disconnectAsync: disconnectEvm } = useDisconnect()
  const { accounts, unlink } = useVenueAccounts()
  const { pacifica: pacificaOwner, phoenix: phoenixOwner, hyperliquid: hyperliquidOwner, aster: asterOwner } = accounts
  const storage = browserStorage()

  return (
    <TradingAgentSession
      storage={storage}
      pacificaOwner={pacificaOwner}
      phoenixOwner={phoenixOwner}
      hyperliquidOwner={hyperliquidOwner}
      asterOwner={asterOwner}
      unlink={unlink}
      connectedSolanaOwner={solana.connected ? solana.publicKey?.toBase58() ?? null : null}
      connectedEvmOwner={evm.isConnected ? evm.address ?? null : null}
      chainId={evm.chainId}
      switchToAuthorizationChain={(targetChainId) => switchChainAsync({ chainId: targetChainId })}
      solanaSignMessage={solana.signMessage}
      solanaSignTransaction={solana.signTransaction}
      signTypedData={signTypedDataAsync}
      signAsterTypedData={(typedData) => typedData.primaryType === 'ApproveBuilder'
        ? signTypedDataAsync(typedData)
        : signTypedDataAsync(typedData)}
      disconnectSolana={() => solana.disconnect()}
      disconnectEvm={async () => {
        for (const { connector } of evmConnections) {
          await disconnectEvm({ connector })
        }
      }}
    >
      {children}
    </TradingAgentSession>
  )
}

function TradingAgentSession({
  children,
  storage,
  pacificaOwner,
  phoenixOwner,
  hyperliquidOwner,
  asterOwner,
  unlink,
  connectedSolanaOwner,
  connectedEvmOwner,
  chainId,
  switchToAuthorizationChain,
  solanaSignMessage,
  solanaSignTransaction,
  signTypedData,
  signAsterTypedData,
  disconnectSolana,
  disconnectEvm,
}: {
  children: ReactNode
  storage: TradingAgentStore
  pacificaOwner: string | null
  phoenixOwner: string | null
  hyperliquidOwner: string | null
  asterOwner: string | null
  unlink: (venue: Venue) => void
  connectedSolanaOwner: string | null
  connectedEvmOwner: string | null
  chainId?: number
  switchToAuthorizationChain: (chainId: typeof mainnet.id | typeof bsc.id) => Promise<unknown>
  solanaSignMessage?: (message: Uint8Array) => Promise<Uint8Array>
  solanaSignTransaction?: OwnerTransactionSigner
  signTypedData: ReturnType<typeof useSignTypedData>['signTypedDataAsync']
  signAsterTypedData: (typedData: AsterApprovalTypedData | AsterDataAgentApprovalTypedData) => Promise<Hex>
  disconnectSolana: () => Promise<void>
  disconnectEvm: () => Promise<void>
}) {
  const [pacifica, setPacifica] = useState(() => initialState('pacifica', pacificaOwner))
  const [hyperliquid, setHyperliquid] = useState(() => initialState('hyperliquid', hyperliquidOwner))
  const [aster, setAster] = useState(() => initialState('aster', asterOwner))
  const [phoenix, setPhoenix] = useState(() => initialState('phoenix', phoenixOwner))
  const [phoenixDisconnectNoticeOwner, setPhoenixDisconnectNoticeOwner] = useState<string | null>(null)
  const owners = useRef({ pacifica: pacificaOwner, phoenix: phoenixOwner, hyperliquid: hyperliquidOwner, aster: asterOwner })
  const builderApproval = useRef<{ ownerAddress: string; promise: Promise<void> } | null>(null)
  const revokedPacificaAgents = useRef(new Set<string>())
  owners.current = { pacifica: pacificaOwner, phoenix: phoenixOwner, hyperliquid: hyperliquidOwner, aster: asterOwner }

  useEffect(() => restoreTradingAgent('pacifica', pacificaOwner, storage, setPacifica,
    () => ownerStillCurrent('pacifica', pacificaOwner, owners.current)), [pacificaOwner, storage])
  useEffect(() => restoreTradingAgent('phoenix', phoenixOwner, storage, (state) => {
    setPhoenix(state)
    if (phoenixOwner) setPhoenixDisconnectNoticeOwner((owner) => owner === phoenixOwner ? null : owner)
  },
    () => ownerStillCurrent('phoenix', phoenixOwner, owners.current)), [phoenixOwner, storage])
  useEffect(() => restoreTradingAgent('hyperliquid', hyperliquidOwner, storage, setHyperliquid,
    () => ownerStillCurrent('hyperliquid', hyperliquidOwner, owners.current)), [hyperliquidOwner, storage])
  useEffect(() => restoreTradingAgent('aster', asterOwner, storage, setAster,
    () => ownerStillCurrent('aster', asterOwner, owners.current)), [asterOwner, storage])

  const assertOwnerSigner = (venue: Venue) => {
    const solanaVenue = venue === 'pacifica' || venue === 'phoenix'
    const connected = solanaVenue ? connectedSolanaOwner : connectedEvmOwner
    const owner = owners.current[venue]
    const matches = owner && connected && (solanaVenue ? owner === connected : owner.toLowerCase() === connected.toLowerCase())
    if (!matches) throw new Error(`Connect the ${venue} owner wallet first`)
  }

  const authorize = async (venue: Venue, options?: { replaceExistingDelegate?: boolean; expectedOwner?: string; expectedAuthority?: string }) => {
    const setState = venue === 'phoenix' ? setPhoenix : venue === 'pacifica' ? setPacifica : venue === 'aster' ? setAster : setHyperliquid
    const ownerAddress = owners.current[venue]
    if (!ownerAddress) throw new Error(`Connect the ${venue} owner wallet first`)
    assertOwnerSigner(venue)
    if (venue === 'phoenix' && options?.expectedOwner && options.expectedOwner !== ownerAddress) throw new Error('Phoenix owner changed before delegate replacement')
    setState({ venue, ownerAddress, agentAddress: null, status: 'authorizing', error: null })
    try {
      const agent = await withAgentLock(venue, ownerAddress, async () => {
        await storage.ready()
        if (venue === 'phoenix') {
          const { authorizePhoenixAgent, createPhoenixTransport } = await import('./phoenix-agent.ts')
          return authorizePhoenixAgent({ storage, ownerAddress, transport: createPhoenixTransport(), signTransaction: solanaSignTransaction,
            isCurrent: () => ownerStillCurrent('phoenix', ownerAddress, owners.current), replaceExistingDelegate: options?.replaceExistingDelegate, expectedAuthority: options?.expectedAuthority })
        }
        return venue === 'pacifica'
          ? reauthorizePacifica(ownerAddress)
          : venue === 'aster'
            ? authorizeAster(ownerAddress)
            : authorizeHyperliquid(ownerAddress)
      })
      if (venue === 'hyperliquid') {
        try {
          await ensureHyperliquidBuilderFee(ownerAddress)
        } catch {
          // The agent is authorized. Execution preflight will retry the fee approval.
        }
      }
      if (!ownerStillCurrent(venue, ownerAddress, owners.current)) {
        throw new Error(`${venue} owner changed during agent authorization`)
      }
      setState({ venue, ownerAddress, agentAddress: agent.agentAddress, status: 'ready', error: null })
    } catch (error) {
      const message = userErrorMessage(error, 'Authorization failed')
      if (ownerStillCurrent(venue, ownerAddress, owners.current)) {
        setState({ venue, ownerAddress, agentAddress: null, status: venue === 'phoenix' && error instanceof Error && error.name === 'PhoenixPendingError' ? 'pending' : 'error', error: message })
      }
      throw error
    }
  }

  const authorizePacifica = async (ownerAddress: string) => {
    if (!solanaSignMessage) throw new Error('Solana wallet does not support message signing')
    const { authorizePacificaAgent } = await import('./pacifica-agent.ts')
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
    assertOwnerSigner('pacifica')
    if (!solanaSignMessage) throw new Error('Solana wallet does not support message signing')
    const { revokePacificaAgent } = await import('./pacifica-agent.ts')
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
    const { authorizeHyperliquidAgent } = await import('./hyperliquid-agent.ts')
    return authorizeHyperliquidAgent({
      agentName: await fetchAgentApprovalName('hyperliquid'),
      storage,
      ownerAddress,
      chainId: mainnet.id,
      // Defer builder approval until the agent succeeds; fee cancellation must
      // not turn a successful trading-agent authorization into a failure.
      builderFeeApproved: async () => true,
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
    const [
      { authorizeAsterAgent },
      { authorizeAsterDataAgent, prepareAsterDataAgentAuthorization },
    ] = await Promise.all([
      import('./aster-agent.ts'),
      import('./aster-data-agent.ts'),
    ])
    return authorizeAsterAgent({
      agentName: await fetchAgentApprovalName('aster'),
      storage,
      ownerAddress,
      signTypedData: signAsterTypedData,
      builderApproved: hasApprovedAsterBuilder,
      relay: relayAsterAuthorization,
      reconcile: (candidates) => reconcileAsterAuthorization(ownerAddress, candidates),
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
      const { approveHyperliquidBuilderFee, hasApprovedHyperliquidBuilderFee } = await import('./hyperliquid-agent.ts')
      if (await hasApprovedHyperliquidBuilderFee(ownerAddress, hyperliquidBuilderAddress)) return
      assertOwnerSigner('hyperliquid')
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
    const { signWithStoredTradingAgent } = await import('./signing.ts')
    const solanaVenue = request.venue === 'pacifica' || request.venue === 'phoenix'
    const currentOwner = owners.current[request.venue]
    const matches = solanaVenue
      ? currentOwner === request.account
      : currentOwner?.toLowerCase() === request.account.toLowerCase()
    if (!matches) throw new Error(`${request.venue} owner changed during execution`)
    if (request.venue === 'hyperliquid' && (request.action === 'open' || request.action === 'close')) {
      await ensureHyperliquidBuilderFee(request.account)
    }
    if (!ownerStillCurrent(request.venue, request.account, owners.current)) {
      throw new Error(`${request.venue} owner changed during execution`)
    }
    const isCurrent = () => ownerStillCurrent(request.venue, request.account, owners.current)
    const signed = request.venue === 'phoenix'
      ? await withAgentLock('phoenix', request.account, () => signWithStoredTradingAgent(storage, request, isCurrent))
      : await signWithStoredTradingAgent(storage, request)
    if (!ownerStillCurrent(request.venue, request.account, owners.current)) {
      throw new Error(`${request.venue} owner changed during signing`)
    }
    return signed
  }

  const revokePhoenix = async () => {
    const ownerAddress = owners.current.phoenix
    if (!ownerAddress) return
    setPhoenix((current) => ({ ...current, status: 'disconnecting', error: null }))
    try {
      await withAgentLock('phoenix', ownerAddress, async () => {
        const { revokePhoenixAgent, createPhoenixTransport } = await import('./phoenix-agent.ts')
        await revokePhoenixAgent({ storage, ownerAddress, transport: createPhoenixTransport(), signTransaction: solanaSignTransaction ? (transaction) => {
          assertOwnerSigner('phoenix')
          return solanaSignTransaction(transaction)
        } : undefined,
          isCurrent: () => ownerStillCurrent('phoenix', ownerAddress, owners.current) })
      })
      if (ownerStillCurrent('phoenix', ownerAddress, owners.current)) setPhoenix(missingState('phoenix', ownerAddress))
    } catch (error) {
      if (ownerStillCurrent('phoenix', ownerAddress, owners.current)) setPhoenix({ ...missingState('phoenix', ownerAddress), status: error instanceof Error && error.name === 'PhoenixPendingError' ? 'pending' : 'error', error: userErrorMessage(error, 'Phoenix revoke could not be confirmed; key preserved') })
      throw error
    }
  }

  const disconnectVenue = async (venue: Venue) => {
    const ownerAddress = owners.current[venue]
    if (!ownerAddress) return
    if (venue === 'phoenix') {
      try {
        await revokePhoenix()
      } catch {
        // Revoke retains keys/operation metadata; it must not prevent unlinking.
        setPhoenixDisconnectNoticeOwner(ownerAddress)
      }
    } else if (venue === 'pacifica') {
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
    } else {
      const setState = venue === 'aster' ? setAster : setHyperliquid
      setState((current) => ({ ...current, status: 'disconnecting', error: null }))
      try {
        await withAgentLock(venue, ownerAddress, () => storage.clear(venue, ownerAddress))
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unable to disconnect EVM wallet'
        setState((current) => ({ ...current, status: 'error', error: message }))
        throw error
      }
      setState(missingState(venue, ownerAddress))
    }
    if (!ownerStillCurrent(venue, ownerAddress, owners.current)) throw new Error(`${venue} owner changed during disconnect`)
    unlink(venue)
    const solanaVenue = venue === 'pacifica' || venue === 'phoenix'
    const connected = solanaVenue ? connectedSolanaOwner : connectedEvmOwner
    const family: Venue[] = solanaVenue ? ['pacifica', 'phoenix'] : ['hyperliquid', 'aster']
    const stillInUse = connected && family.some((other) => other !== venue && (solanaVenue
      ? owners.current[other] === connected : owners.current[other]?.toLowerCase() === connected.toLowerCase()))
    if (connected && !stillInUse) {
      if (solanaVenue) await disconnectSolana()
      else await disconnectEvm()
    }
  }

  return (
    <TradingAgentContext.Provider value={{
      pacifica, hyperliquid, aster, phoenix, phoenixDisconnectNoticeOwner, authorize, sign, disconnectVenue, revokePhoenix,
      ensureHyperliquidTradingApproval: ensureHyperliquidBuilderFee,
    }}>
      {children}
    </TradingAgentContext.Provider>
  )
}

function ownerStillCurrent(
  venue: Venue,
  expectedOwner: string | null,
  owners: Record<Venue, string | null>,
): boolean {
  const current = owners[venue]
  if (expectedOwner === null) return current === null
  return venue === 'pacifica' || venue === 'phoenix'
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
  const normalizedOwner = venue === 'pacifica' || venue === 'phoenix' ? ownerAddress : ownerAddress.toLowerCase()
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

async function relayAsterAuthorization(request: AsterApproveAgentRequest): Promise<'accepted' | 'uncertain'> {
  let response: Response
  try {
    response = await apiFetch('/api/v1/live/agents/aster/approve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(request),
    })
  } catch {
    return 'uncertain'
  }
  if (response.status === 202) return 'uncertain'
  if (response.ok) return 'accepted'
  const body = await response.json().catch(() => null) as { error?: string } | null
  throw apiError(response.status, 'Unable to authorize the Aster trading agent. Please try again.', body)
}

async function reconcileAsterAuthorization(ownerAddress: string, candidates: string[]): Promise<string> {
  const response = await apiFetch('/api/v1/live/agents/aster/reconcile', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ account: ownerAddress, candidates }),
  })
  if (!response.ok) {
    const body = await response.json().catch(() => null) as { error?: string } | null
    throw apiError(response.status, 'Unable to verify Aster authorization. Please try again.', body)
  }
  const body = await response.json() as { agent_address?: unknown }
  if (typeof body.agent_address !== 'string') throw new Error('Aster returned an invalid agent status')
  return body.agent_address
}

async function hasApprovedAsterBuilder(ownerAddress: string): Promise<boolean> {
  const response = await apiFetch(`/api/v1/live/agents/aster/builder-approval?account=${encodeURIComponent(ownerAddress)}`)
  if (!response.ok) throw apiError(response.status, 'Unable to verify Aster builder approval. Please try again.')
  const body = await response.json() as { approved?: unknown }
  if (typeof body.approved !== 'boolean') throw new Error('Aster returned an invalid builder approval status')
  return body.approved
}

async function hasApprovedPacificaBuilderCode(ownerAddress: string): Promise<boolean> {
  const response = await apiFetch(`/api/v1/live/agents/pacifica/builder-code-approval?account=${encodeURIComponent(ownerAddress)}`)
  if (!response.ok) throw apiError(response.status, 'Unable to verify Pacifica builder approval. Please try again.')
  const body = await response.json() as { approved?: unknown }
  if (typeof body.approved !== 'boolean') throw new Error('Pacifica returned an invalid builder approval status')
  return body.approved
}
