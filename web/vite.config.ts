import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
    sourcemap: false,
    rolldownOptions: {
      output: {
        // React and MUI do not change between deploys, but application code
        // does. Kept in one file with the app, a returning visitor re-downloads
        // the whole framework to pick up a one line fix; split out, the cached
        // vendor chunk survives the deploy.
        manualChunks: (id: string) => {
          if (!id.includes('node_modules')) return undefined
          if (id.includes('@mui')) return 'mui'
          if (id.includes('react-router') || id.includes('/react/') || id.includes('react-dom') || id.includes('scheduler')) return 'react'
          return undefined
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
      '/mcp': 'http://localhost:8080',
    },
  },
})
