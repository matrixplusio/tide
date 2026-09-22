import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In the dev cluster the page is served through ingress at tide.example.test:443 (https),
// so HMR must connect back through the same port. Source is a hostPath mount
// where inotify events do not cross the VM boundary, hence polling.
export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
    // The dev cluster serves this through ingress under whatever wildcard
    // domain the laptop resolves; TIDE_DEV_HOST overrides it (see local.mk).
    allowedHosts: [process.env.TIDE_DEV_HOST ?? 'tide.example.test', 'localhost'],
    hmr: process.env.VITE_HMR_CLIENT_PORT
      ? { clientPort: Number(process.env.VITE_HMR_CLIENT_PORT), protocol: process.env.VITE_HMR_PROTOCOL === 'wss' ? 'wss' : 'ws' }
      : undefined,
    watch: process.env.VITE_POLL ? { usePolling: true, interval: 300 } : undefined,
    // Every API path, including the SSO browser redirects, lives under /api.
    proxy: { '/api': 'http://localhost:8080' },
  },
  build: { outDir: '../internal/web/dist', emptyOutDir: true },
})
