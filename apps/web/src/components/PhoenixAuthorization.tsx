import { useState } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useTradingAgents } from '@/hooks/useTradingAgents'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { accountActionStyles } from './account-action-styles'

// Phoenix-specific actions live inside the shared account card. Delegate
// replacement and confirmed revoke retain their existing owner-bound lifecycle.
export function PhoenixAuthorization({ disabled, hasActiveExposure, onConnect, onDisconnect }: {
  disabled: boolean; hasActiveExposure: boolean; onConnect: () => void; onDisconnect?: () => void
}) {
  const agents = useTradingAgents()
  const wallet = useWallet()
  const owner = wallet.connected ? wallet.publicKey?.toBase58() : null
  const state = agents.phoenix
  const [replacement, setReplacement] = useState<{ owner: string; authority: string } | null>(null)
  const [revokeOwner, setRevokeOwner] = useState<string | null>(null)
  const busy = ['restoring', 'authorizing', 'disconnecting'].includes(state.status)
  const authorized = state.status === 'ready'
  const canRevoke = authorized || state.status === 'pending' || !!state.agentAddress
  const authorizeLabel = authorized ? 'Reauthorize'
    : state.status === 'pending' ? 'Resume authorization'
    : state.status === 'restoring' ? 'Restoring authorization...'
    : state.status === 'authorizing' ? 'Authorizing agent...'
    : state.status === 'disconnecting' ? 'Revoking authorization...'
    : state.status === 'error' ? 'Reauthorize Phoenix' : 'Authorize Phoenix'

  const authorize = async (replace = false, expectedOwner = owner, expectedAuthority?: string) => {
    try {
      await agents.authorize('phoenix', { replaceExistingDelegate: replace, expectedOwner: expectedOwner ?? undefined, expectedAuthority })
    } catch (error) {
      if (owner && error instanceof Error && error.name === 'PhoenixReplacementRequired' && 'authority' in error && typeof error.authority === 'string') {
        setReplacement({ owner, authority: error.authority })
      }
    }
  }

  return <>
    <div className="flex flex-col gap-1.5">
      {state.error && <p className="text-[10px] text-red-400" role="alert">{state.error}</p>}
      {owner && !wallet.signTransaction && <p className="text-[10px] text-muted-foreground">This wallet must support transaction signing.</p>}
      <div className="flex items-center justify-end">
        {!owner ? <button type="button" className={accountActionStyles.primary} onClick={onConnect}>Connect</button> : (
          <div className="flex items-center gap-1.5 flex-wrap justify-end">
            <button type="button" className={authorized ? accountActionStyles.secondary : accountActionStyles.primary}
              disabled={disabled || busy || !wallet.signTransaction} onClick={() => void authorize()}>
              {authorizeLabel}
            </button>
            {canRevoke && <button type="button" className={accountActionStyles.secondary}
              disabled={disabled || busy || !wallet.signTransaction} onClick={() => {
                if (hasActiveExposure) setRevokeOwner(owner)
                else void agents.revokePhoenix().catch(() => {})
              }}>Revoke</button>}
            {onDisconnect && <button type="button" className={accountActionStyles.secondary}
              disabled={disabled || busy} onClick={onDisconnect}>Disconnect</button>}
          </div>
        )}
      </div>
    </div>
    <Dialog open={!!revokeOwner && revokeOwner === owner} onOpenChange={(open) => { if (!open) setRevokeOwner(null) }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Revoke Phoenix authorization?</DialogTitle>
          <DialogDescription>You have active Phoenix exposure. Orbital will lose trading access to your Phoenix account. Positions remain open; close them first or manage them directly on Phoenix.</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => setRevokeOwner(null)}>Cancel</Button>
          <Button disabled={disabled || busy || !wallet.signTransaction} onClick={() => {
            const confirmedOwner = revokeOwner
            setRevokeOwner(null)
            if (confirmedOwner && confirmedOwner === owner) void agents.revokePhoenix().catch(() => {})
          }}>Revoke authorization</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
    <Dialog open={!!replacement && replacement.owner === owner} onOpenChange={(open) => { if (!open) setReplacement(null) }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Replace the existing Phoenix delegate?</DialogTitle>
          <DialogDescription>This replaces the current position authority for your shared cross account. The existing delegate will lose trading access; positions and orders remain.</DialogDescription>
        </DialogHeader>
        <p className="break-all font-mono text-xs">{replacement?.authority}</p>
        <DialogFooter>
          <Button variant="outline" onClick={() => setReplacement(null)}>Cancel</Button>
          <Button disabled={disabled || busy} onClick={() => { const confirmed = replacement; setReplacement(null); if (confirmed) void authorize(true, confirmed.owner, confirmed.authority) }}>Replace delegate</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </>
}
