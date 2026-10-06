import { defineConfig } from '@playwright/test';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: 'http://localhost:5174',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure'
  },
  reporter: [['list'], ['html', { open: 'never' }]],
  webServer: [
    {
      command: 'go run ./cmd/ops',
      cwd: fileURLToPath(new URL('..', import.meta.url)),
      url: 'http://127.0.0.1:2062/health',
      timeout: 60_000,
      reuseExistingServer: false,
      env: {
        DEMO_MODE: 'true',
        INFRA_LAB: 'true',
        IMESSAGE_LAB: 'true',
        DATABASE_URL:
          'postgres://ops_app:ops_demo_app@127.0.0.1:55443/covent_ops_test?sslmode=disable',
        MIGRATION_DATABASE_URL:
          'postgres://ops_admin:ops_demo_admin@127.0.0.1:55443/covent_ops_test?sslmode=disable',
        REDIS_URL: 'redis://127.0.0.1:56390/0',
        LISTEN_ADDR: '127.0.0.1:2062',
        ALLOWED_ORIGIN: 'http://localhost:5174'
      }
    },
    {
      command: 'npm run dev -- --port 5174',
      url: 'http://localhost:5174',
      reuseExistingServer: false,
      env: { OPS_API_URL: 'http://127.0.0.1:2062' }
    }
  ]
});
