import { defineConfig, devices } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
export default defineConfig({
  testDir: './e2e', fullyParallel: false, workers: 1, timeout: 30000,
  use: { baseURL: 'http://localhost:5174', trace: 'retain-on-failure', launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH} },
  projects: [{name: 'desktop', use: {...devices['Desktop Chrome']}}, {name: 'mobile', use: {...devices['iPhone 13'], defaultBrowserType: 'chromium'}}],
  webServer: [
    {command: '../backend/bin/server', url: 'http://127.0.0.1:8081/healthz', env: {HTTP_ADDR: '127.0.0.1:8081', FRONTEND_ORIGIN: 'http://localhost:5174', DATA_DIR: mkdtempSync(join(tmpdir(), 'studio-e2e-'))}, reuseExistingServer: false},
    {command: 'npm run dev -- --port 5174', url: 'http://localhost:5174', env: {VITE_API_BASE_URL: 'http://localhost:8081'}, reuseExistingServer: false},
  ],
})
