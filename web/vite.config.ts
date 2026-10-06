import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: process.env.OPS_API_URL || 'http://127.0.0.1:2061' },
      '/health': { target: process.env.OPS_API_URL || 'http://127.0.0.1:2061' }
    }
  },
  preview: {
    proxy: { '/api': { target: process.env.OPS_API_URL || 'http://127.0.0.1:2061' } }
  }
});
