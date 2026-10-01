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

function routeVenues(position: LivePosition, fills: LiveFillDetail[]) {
  const long = fills.find((fill) => fill.filled && fill.side.toLowerCase() === 'long')
  const short = fills.find((fill) => fill.filled && fill.side.toLowerCase() === 'short')
  if (long && short) {
    return [
      { side: 'LONG', metadata: venueMetadata(long.venue) },
      { side: 'SHORT', metadata: venueMetadata(short.venue) },
    ]
  }
  return [
    { side: 'VENUE 1', metadata: venueMetadata(position.venue_a) },
    { side: 'VENUE 2', metadata: venueMetadata(position.venue_b) },
  ]
}

function loadLogo(src: string | null): Promise<HTMLImageElement | null> {
  if (!src) return Promise.resolve(null)
  return new Promise((resolve) => {
    const image = new Image()
    image.onload = () => resolve(image)
    image.onerror = () => resolve(null)
    image.src = src
  })
}

function drawOrbitalMark(ctx: CanvasRenderingContext2D, x: number, y: number, size: number) {
  ctx.save()
  ctx.translate(x + size / 2, y + size / 2)
  ctx.strokeStyle = 'rgba(148, 163, 184, 0.72)'
  ctx.lineWidth = 1.5
  ctx.rotate(-0.38)
  ctx.beginPath()
  ctx.ellipse(0, 0, size / 2, size / 4, 0, 0, Math.PI * 2)
  ctx.stroke()
  ctx.rotate(0.95)
  ctx.beginPath()
  ctx.ellipse(0, 0, size / 3.2, size / 5, 0, 0, Math.PI * 2)
  ctx.stroke()
  ctx.fillStyle = '#22d3ee'
  ctx.shadowColor = 'rgba(34, 211, 238, 0.8)'
  ctx.shadowBlur = 10
  ctx.beginPath()
  ctx.arc(0, 0, 5, 0, Math.PI * 2)
  ctx.fill()
  ctx.restore()
}

async function createClosedPositionCard(position: LivePosition, fills: LiveFillDetail[]): Promise<Omit<ShareImage, 'url'>> {
  const metrics = closedPositionShareMetrics(position)
  if (!metrics) throw new Error('Unable to calculate closed-position performance')
  const route = routeVenues(position, fills)
  const logos = await Promise.all(route.map(({ metadata }) => loadLogo(metadata.logo)))
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

  ctx.lineWidth = 1
  for (let x = 0; x <= canvas.width; x += 80) {
    ctx.strokeStyle = x % 320 === 0 ? 'rgba(103, 232, 249, 0.09)' : 'rgba(103, 232, 249, 0.055)'
    ctx.beginPath()
    ctx.moveTo(x, 0)
    ctx.lineTo(x, canvas.height)
    ctx.stroke()
  }
  for (let y = 0; y <= canvas.height; y += 70) {
    ctx.strokeStyle = y % 280 === 0 ? 'rgba(103, 232, 249, 0.09)' : 'rgba(103, 232, 249, 0.055)'
    ctx.beginPath()
    ctx.moveTo(0, y)
    ctx.lineTo(canvas.width, y)
    ctx.stroke()
  }

  drawOrbitalMark(ctx, 72, 55, 40)
  ctx.fillStyle = '#f8fafc'
  ctx.font = "650 34px 'Geist Variable', system-ui, sans-serif"
  ctx.fillText('ORBITAL MARKETS', 128, 84)
  ctx.fillStyle = '#64748b'
  ctx.font = '600 15px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.textAlign = 'right'
  ctx.fillText('CLOSED POSITION', 1128, 82)
  ctx.textAlign = 'left'

  ctx.fillStyle = '#67e8f9'
  ctx.font = '600 18px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText(position.asset.toUpperCase(), 72, 205)
  ctx.fillStyle = '#f8fafc'
  ctx.font = "700 104px 'Geist Variable', system-ui, sans-serif"
  ctx.fillStyle = metrics.roi >= 0 ? '#4ade80' : '#fb7185'
  ctx.fillText(fmtReturn(metrics.heroValue), 66, 342)
  ctx.fillStyle = '#64748b'
  ctx.font = '600 16px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText(metrics.heroLabel, 72, 382)

  ctx.fillText('ROUTE', 744, 205)
  route.forEach(({ side, metadata }, index) => {
    const x = 744 + index * 205
    ctx.fillStyle = 'rgba(255, 255, 255, 0.045)'
    ctx.strokeStyle = 'rgba(255, 255, 255, 0.10)'
    ctx.lineWidth = 1
    ctx.beginPath()
    ctx.roundRect(x, 232, 112, 112, 18)
    ctx.fill()
    ctx.stroke()
    const logo = logos[index]
    if (logo) {
      ctx.drawImage(logo, x + 22, 254, 68, 68)
    } else {
      ctx.fillStyle = metadata.color
      ctx.font = '700 25px ui-monospace, SFMono-Regular, Menlo, monospace'
      ctx.textAlign = 'center'
      ctx.fillText(metadata.shortLabel, x + 56, 300)
      ctx.textAlign = 'left'
    }
    ctx.fillStyle = index === 0 ? '#4ade80' : '#fb7185'
    ctx.font = '600 14px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.textAlign = 'center'
    ctx.fillText(side, x + 56, 374)
    ctx.textAlign = 'left'
  })
  ctx.fillStyle = '#475569'
  ctx.font = '500 30px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText('→', 885, 300)

  ctx.fillStyle = '#64748b'
  ctx.font = '600 15px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText('HOLD', 744, 446)
  ctx.fillStyle = '#e2e8f0'
  ctx.font = '650 34px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText(metrics.holdDuration, 744, 490)

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
