import { isolatedServerEnv } from './playwright-server-env'
import { defineConfig, devices } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
export default defineConfig({
  testDir: './e2e-auth', fullyParallel: false, workers: 1, timeout: 30000,
  use: { locale: 'zh-CN', baseURL: 'http://localhost:5175', channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL, trace: 'retain-on-failure', launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH} },
  projects: [{name: 'desktop', use: {...devices['Desktop Chrome']}}, {name: 'mobile', use: {...devices['iPhone 13'], defaultBrowserType: 'chromium'}}],
  webServer: [
    {command: '../backend/bin/server', url: 'http://127.0.0.1:8082/healthz', env: {...isolatedServerEnv, AUTH_MODE: 'secure', AUTH_ALLOW_REGISTRATION: 'true', AUTH_COOKIE_SECURE: 'false', HTTP_ADDR: '127.0.0.1:8082', FRONTEND_ORIGIN: 'http://localhost:5175', DATA_DIR: mkdtempSync(join(tmpdir(), 'studio-auth-e2e-')), IMAGE_PROVIDER: 'mock-svg', TEXT_PROVIDER: 'mock-text', SPEECH_PROVIDER: '', VIDEO_PROVIDER: '', ASR_PROVIDER: '', OPENAI_API_KEY: ''}, reuseExistingServer: false},
    {command: 'npm run dev -- --port 5175', url: 'http://localhost:5175', env: {VITE_API_BASE_URL: 'http://localhost:8082'}, reuseExistingServer: false},
  ],
})
