import { isolatedServerEnv } from './playwright-server-env'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { defineConfig, devices } from '@playwright/test'
// Backend and independent worker are created and cleaned up by
// scripts/postgres_browser_check.py against a newly created synthetic database.
export default defineConfig({
  outputDir: '/tmp/studio-quote-playwright-results', testDir: './e2e-quotes', fullyParallel: false, workers: 1, timeout: 45000,
  use: {locale: 'zh-CN', baseURL: 'http://localhost:5175', trace: 'retain-on-failure', launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}},
  projects: [{name: 'desktop', use: {...devices['Desktop Chrome']}}, {name: 'mobile', use: {...devices['iPhone 13'], defaultBrowserType: 'chromium'}}],
  webServer: [{command: 'npm run dev -- --port 5175', url: 'http://localhost:5175', env: {...isolatedServerEnv, npm_config_cache: mkdtempSync(join(tmpdir(), 'studio-pg-browser-npm-')), VITE_API_BASE_URL: 'http://localhost:8082'}, reuseExistingServer: false}],
})
