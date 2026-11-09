import { useState } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useTradingAgents } from '@/hooks/useTradingAgents'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'

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
    <div className="flex flex-col gap-2">
      {owner && <p className="text-xs" role="status">{state.status === 'ready' ? 'Agent ready — verified on each action' : state.status === 'pending' ? 'Pending verification — key preserved' : state.status === 'restoring' ? 'Restoring local authorization…' : state.status === 'authorizing' ? 'Awaiting owner approval / confirmation…' : state.status === 'disconnecting' ? 'Confirming revoke…' : 'Authorization required'}</p>}
      {state.error && <p className="text-xs text-destructive" role="alert">{state.error}</p>}
      {owner && !wallet.signTransaction && <p className="text-xs text-muted-foreground">This wallet must support transaction signing.</p>}
      <div className="flex flex-wrap justify-end gap-2">
        {!owner ? <Button size="xs" onClick={onConnect}>Connect</Button> : <>
          <Button size="xs" disabled={disabled || busy || !wallet.signTransaction} onClick={() => void authorize()}>
            {state.status === 'ready' ? 'Reauthorize Phoenix' : state.status === 'pending' ? 'Resume Phoenix' : 'Authorize Phoenix'}
          </Button>
          <Button size="xs" variant="outline" disabled={disabled || busy || state.status === 'missing' || !wallet.signTransaction} onClick={() => {
            if (hasActiveExposure) setRevokeOwner(owner)
            else void agents.revokePhoenix().catch(() => {})
          }}>Revoke Phoenix</Button>
          {onDisconnect && <Button size="xs" variant="ghost" disabled={disabled || busy} onClick={onDisconnect}>Disconnect Solana</Button>}
        </>}
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
