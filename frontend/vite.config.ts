import { svelte } from '@sveltejs/vite-plugin-svelte'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [svelte()],
  build: {
    emptyOutDir: true,
    outDir: 'dist',
    sourcemap: false
  }
})
