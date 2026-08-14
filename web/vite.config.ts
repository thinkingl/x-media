import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 18091,
    proxy: {
      '/api': {
        target: 'http://localhost:18090',
        changeOrigin: true,
      },
      '/uploads': {
        target: 'http://localhost:18090',
        changeOrigin: true,
      },
      '/live': {
        target: 'http://localhost:18090',
        changeOrigin: true,
      },
    },
  },
})
