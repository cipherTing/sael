import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: { proxy: { '/admin': 'http://localhost:8080' } },
  test: { environment: 'jsdom', testTimeout: 10000 }
})
