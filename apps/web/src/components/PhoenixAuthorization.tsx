import { useState } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useTradingAgents } from '@/hooks/useTradingAgents'
import { useVenueReadiness } from '@/hooks/useVenueReadiness'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'

export function PhoenixAuthorization({ disabled, onConnect }: { disabled: boolean; onConnect: () => void }) {
  const agents = useTradingAgents()
  const wallet = useWallet()
  const owner = wallet.connected ? wallet.publicKey?.toBase58() : null
  const state = agents.phoenix
  const { phoenix: readiness } = useVenueReadiness()
  const [replacement, setReplacement] = useState<{ owner: string; authority: string } | null>(null)
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
    <Card size="sm">
      <CardHeader>
        <CardTitle>Phoenix <Badge variant="secondary">{readiness.status === 'ready' ? 'Live ready' : 'Not ready'}</Badge></CardTitle>
        <CardDescription>Trade using your existing Phoenix cross account.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <p className="text-xs text-muted-foreground">Authorize a browser-local agent for your existing Phoenix cross account. Disconnect revokes on-chain before deleting the local key.</p>
        {owner && <p className="truncate font-mono text-xs" title={owner}>{owner}</p>}
        <p className="text-xs" role="status">{!owner ? 'Connect a Solana owner wallet' : state.status === 'ready' ? 'Agent authorized' : state.status === 'pending' ? 'Pending verification — key preserved' : state.status === 'restoring' ? 'Verifying on-chain authority…' : state.status === 'authorizing' ? 'Awaiting owner approval / confirmation…' : state.status === 'disconnecting' ? 'Confirming revoke…' : 'Authorization required'}</p>
        {state.error && <p className="text-xs text-destructive" role="alert">{state.error}</p>}
        {state.status === 'ready' && readiness.blockingReasons.map((reason) => <p key={reason} className="text-xs text-muted-foreground">{reason}</p>)}
        {owner && !wallet.signTransaction && <p className="text-xs text-muted-foreground">This wallet must support transaction signing.</p>}
      </CardContent>
      <CardFooter className="flex-wrap justify-end gap-2">
        {!owner ? <Button size="xs" onClick={onConnect}>Connect Solana</Button> : <>
          <Button size="xs" disabled={disabled || busy || !wallet.signTransaction} onClick={() => void authorize()}>
            {state.status === 'ready' ? 'Reauthorize Phoenix' : state.status === 'pending' ? 'Resume Phoenix' : 'Authorize Phoenix'}
          </Button>
          <Button size="xs" variant="outline" disabled={disabled || busy || state.status === 'missing' || !wallet.signTransaction} onClick={() => void agents.revokePhoenix().catch(() => {})}>Revoke Phoenix</Button>
        </>}
      </CardFooter>
    </Card>
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
