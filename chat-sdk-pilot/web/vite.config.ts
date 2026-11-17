import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'node:path'

const upstream = process.env.PILOT_FRONTEND_AGGREGATE
if (!upstream) throw new Error('PILOT_FRONTEND_AGGREGATE must name the pinned frontend checkout')
const packages = resolve(upstream, 'ts/design-kit/packages')

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: [
      { find: /^@hollis-labs\/kit-chat$/, replacement: resolve(packages, 'kit-chat/dist/index.js') },
      { find: /^@hollis-labs\/kit-cards$/, replacement: resolve(packages, 'kit-cards/dist/index.js') },
    ],
  },
  build: { outDir: 'dist' },
})
