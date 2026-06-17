import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
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
})
