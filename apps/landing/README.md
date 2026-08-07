# Orbital landing

Public landing page for `orbitalmarkets.xyz`. The gated trading application remains at `app.orbitalmarkets.xyz`.

## Local development

```bash
pnpm install
pnpm dev
```

Copy `.env.example` to `.env.local` when overriding the app or waitlist endpoint.

## Cloudflare Pages

- Root directory: `apps/landing`
- Build command: `pnpm build`
- Output directory: `dist`
- Custom domain: `orbitalmarkets.xyz`

The waitlist form posts JSON to `VITE_WAITLIST_ENDPOINT`, defaulting to `/api/waitlist`. Bind that path to the access Worker before deploying the page publicly.
