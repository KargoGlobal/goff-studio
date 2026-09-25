import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': new URL('./src', import.meta.url).pathname },
  },
  build: {
    outDir: '../cmd/goff-studio/dist',
    emptyOutDir: true,
    rolldownOptions: {
      output: {
        // Framework code changes rarely, so it gets its own long-lived chunk; the
        // rule builder's libraries only load with the flag detail page.
        codeSplitting: {
          groups: [
            { name: 'querybuilder', priority: 10, test: /node_modules[\\/](react-querybuilder|@react-querybuilder|react-dnd|dnd-core|react-dnd-html5-backend)/ },
            { name: 'vendor', priority: 20, test: /node_modules[\\/](react|react-dom|scheduler|react-router|react-router-dom|@tanstack)[\\/]/ },
          ],
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/auth': 'http://localhost:8080',
    },
  },
})
