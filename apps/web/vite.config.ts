import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'
import { rmSync } from 'node:fs'

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), ...(mode === 'staging' ? [{
    name: 'workers-spa-fallback',
    closeBundle() {
      // Pages' catch-all rewrite conflicts with Workers Static Assets' native SPA fallback.
      rmSync(path.resolve(__dirname, 'dist/_redirects'), { force: true })
    },
  }] : [])],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      // For local gate-worker testing only; in prod the Worker is bound to
      // app.<domain>/gate/* directly, no proxy involved.
      '/gate': 'http://localhost:8787',
    },
  },
}))
