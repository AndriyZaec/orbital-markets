import { useEffect, useState } from 'react'
import { CopyIcon, DownloadIcon, Share2Icon, XIcon } from 'lucide-react'
import type { LivePosition } from '@/hooks/useLivePositions'
import type { LiveFillDetail } from '@/hooks/useLivePositionDetail'
import { useLivePositionDetail } from '@/hooks/useLivePositionDetail'
import { closedPositionShareMetrics } from '@/lib/closed-position-share'
import { venueMetadata } from '@/lib/venue-metadata'
import { Button } from '@/components/ui/button'
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  position: LivePosition
  fills?: LiveFillDetail[]
}

interface ShareImage {
  blob: Blob
  file: File
  url: string
}

function fmtReturn(value: number) {
  const percent = value * 100
  return `${percent >= 0 ? '+' : ''}${percent.toFixed(Math.abs(percent) >= 100 ? 0 : 2)}%`
}

function routeLabel(position: LivePosition, fills: LiveFillDetail[]): string {
  const long = fills.find((fill) => fill.filled && fill.side.toLowerCase() === 'long')
  const short = fills.find((fill) => fill.filled && fill.side.toLowerCase() === 'short')
  if (long && short) {
    return `LONG ${venueMetadata(long.venue).shortLabel}  /  SHORT ${venueMetadata(short.venue).shortLabel}`
  }
  return `${venueMetadata(position.venue_a).shortLabel}  /  ${venueMetadata(position.venue_b).shortLabel}`
}

async function createClosedPositionCard(position: LivePosition, fills: LiveFillDetail[]): Promise<Omit<ShareImage, 'url'>> {
  const metrics = closedPositionShareMetrics(position)
  if (!metrics) throw new Error('Unable to calculate closed-position performance')
  const canvas = document.createElement('canvas')
  canvas.width = 1200
  canvas.height = 630
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('Unable to create performance card')

  const background = ctx.createLinearGradient(0, 0, canvas.width, canvas.height)
  background.addColorStop(0, '#070a10')
  background.addColorStop(0.65, '#0a111b')
  background.addColorStop(1, metrics.roi >= 0 ? '#071712' : '#17090d')
  ctx.fillStyle = background
  ctx.fillRect(0, 0, canvas.width, canvas.height)

  const glow = ctx.createRadialGradient(1040, 80, 0, 1040, 80, 560)
  glow.addColorStop(0, metrics.roi >= 0 ? 'rgba(34,197,94,0.18)' : 'rgba(244,63,94,0.18)')
  glow.addColorStop(1, 'rgba(0,0,0,0)')
  ctx.fillStyle = glow
  ctx.fillRect(0, 0, canvas.width, canvas.height)

  ctx.fillStyle = '#67e8f9'
  ctx.font = '600 18px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText('ORBITAL MARKETS  /  CLOSED POSITION', 72, 78)
  ctx.fillStyle = '#f8fafc'
  ctx.font = "700 52px 'Geist Variable', system-ui, sans-serif"
  ctx.fillText(position.asset, 72, 158)
  ctx.fillStyle = '#94a3b8'
  ctx.font = '600 17px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText(routeLabel(position, fills), 72, 198)

  ctx.fillStyle = '#64748b'
  ctx.font = '600 16px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText(metrics.heroLabel, 72, 292)
  ctx.fillStyle = metrics.roi >= 0 ? '#4ade80' : '#fb7185'
  ctx.font = "700 104px 'Geist Variable', system-ui, sans-serif"
  ctx.fillText(fmtReturn(metrics.heroValue), 66, 406)

  ctx.fillStyle = '#64748b'
  ctx.font = '600 15px ui-monospace, SFMono-Regular, Menlo, monospace'
  if (metrics.heroLabel === 'APR') {
    ctx.fillText('ACTUAL ROI', 760, 300)
    ctx.fillStyle = '#4ade80'
    ctx.font = '650 34px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.fillText(fmtReturn(metrics.roi), 760, 342)
  }
  ctx.fillStyle = '#64748b'
  ctx.font = '600 15px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText('HOLD', 760, 390)
  ctx.fillStyle = '#e2e8f0'
  ctx.font = '650 34px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText(metrics.holdDuration, 760, 432)

  ctx.fillStyle = '#475569'
  ctx.font = "400 16px 'Geist Variable', system-ui, sans-serif"
  ctx.fillText('orbital.markets', 72, 568)

  const blob = await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((value) => value ? resolve(value) : reject(new Error('Unable to encode performance card')), 'image/png')
  })
  return { blob, file: new File([blob], `${position.asset.toLowerCase()}-closed-position.png`, { type: 'image/png' }) }
}

export function ClosedPositionShareDialog({ open, onOpenChange, position, fills }: Props) {
  const [image, setImage] = useState<ShareImage | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const detail = useLivePositionDetail(
    fills === undefined && open ? position.id : null,
    [position.venue_a, position.venue_b],
  )
  const resolvedFills = fills ?? detail.data?.fills

  useEffect(() => {
    if (!open || resolvedFills === undefined) return
    let cancelled = false
    let url: string | null = null
    setImage(null)
    setMessage(null)
    createClosedPositionCard(position, resolvedFills).then((result) => {
      url = URL.createObjectURL(result.blob)
      if (cancelled) {
        URL.revokeObjectURL(url)
        return
      }
      setImage({ ...result, url })
    }).catch(() => {
      if (!cancelled) setMessage('Unable to prepare this position card.')
    })
    return () => {
      cancelled = true
      if (url) URL.revokeObjectURL(url)
    }
  }, [open, position, resolvedFills])

  const copyImage = async () => {
    if (!image) return
    try {
      if (!navigator.clipboard?.write || typeof ClipboardItem === 'undefined') throw new Error('Unsupported')
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': image.blob })])
      setMessage('Image copied to clipboard.')
    } catch {
      setMessage('This browser cannot copy images. Download the PNG instead.')
    }
  }
  const downloadImage = () => {
    if (!image) return
    const link = document.createElement('a')
    link.href = image.url
    link.download = image.file.name
    link.click()
    setMessage('PNG downloaded.')
  }
  const canShare = Boolean(image && navigator.canShare?.({ files: [image.file] }) && navigator.share)
  const nativeShare = async () => {
    if (!image) return
    try {
      await navigator.share({ title: `${position.asset} closed position`, files: [image.file] })
    } catch (error) {
      if (!(error instanceof DOMException && error.name === 'AbortError')) setMessage('System sharing is unavailable.')
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="bg-[#080d15] text-slate-100 sm:max-w-2xl" showCloseButton={false}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.14em]">
            <Share2Icon className="size-4 text-cyan-400" /> Share closed position
          </DialogTitle>
        </DialogHeader>
        <DialogClose render={<Button variant="ghost" size="icon-sm" className="absolute top-2 right-2" />}>
          <XIcon /><span className="sr-only">Close</span>
        </DialogClose>
        {image ? <img src={image.url} alt={`${position.asset} closed position share card`} className="w-full rounded-lg ring-1 ring-white/10" /> : (
          <div className="flex aspect-[40/21] items-center justify-center rounded-lg bg-muted/40 text-sm text-muted-foreground">{message ?? detail.error ?? 'Preparing preview...'}</div>
        )}
        {message && image && <p role="status" className="text-xs text-muted-foreground">{message}</p>}
        <DialogFooter className="border-white/[0.07] bg-[#080d15] sm:justify-between">
          <div className="flex flex-wrap gap-2">
            <Button variant="ghost" onClick={copyImage} disabled={!image}><CopyIcon data-icon="inline-start" />Copy image</Button>
            <Button variant="ghost" onClick={downloadImage} disabled={!image}><DownloadIcon data-icon="inline-start" />Download PNG</Button>
          </div>
          {canShare && <Button variant="ghost" onClick={nativeShare}><Share2Icon data-icon="inline-start" />Share image</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
