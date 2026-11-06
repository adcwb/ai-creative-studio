import { isolatedServerEnv } from './playwright-server-env'
import { defineConfig, devices } from '@playwright/test'
import {execFileSync} from 'node:child_process'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const authDataDir=mkdtempSync(join(tmpdir(),'studio-auth-e2e-'))
const adminIDs=['desktop','mobile'].map(device=>JSON.parse(execFileSync('../backend/bin/bootstrap-user',['-email',`p0-${device}@example.invalid`,'-confirm-stopped'],{encoding:'utf8',input:'Synthetic-P0-passphrase!\n',env:{...isolatedServerEnv,DATA_DIR:authDataDir}})).user_id).join(',')
export default defineConfig({
  metadata: {secretStorageAvailable: true},
  testDir: './e2e-auth', fullyParallel: false, workers: 1, timeout: 30000,
  use: { locale: 'zh-CN', baseURL: 'http://localhost:5175', channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL, trace: 'retain-on-failure', launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH} },
  projects: [{name: 'desktop', use: {...devices['Desktop Chrome']}}, {name: 'mobile', use: {...devices['iPhone 13'], defaultBrowserType: 'chromium'}}],
  webServer: [
    {command: 'node e2e-auth/attacker.mjs', url: 'http://localhost:5185', reuseExistingServer: false},
    {command: '../backend/bin/server', url: 'http://127.0.0.1:8082/healthz', env: {...isolatedServerEnv, STUDIO_ADMIN_EMAILS: 'p0-desktop@example.invalid,p0-mobile@example.invalid', STUDIO_ADMIN_USER_IDS:adminIDs, AUTH_MODE: 'secure', AUTH_ALLOW_REGISTRATION: 'true', AUTH_COOKIE_SECURE: 'false', HTTP_ADDR: '127.0.0.1:8082', FRONTEND_ORIGIN: 'http://localhost:5175', DATA_DIR: authDataDir, IMAGE_PROVIDER: 'mock-svg', TEXT_PROVIDER: 'mock-text', SPEECH_PROVIDER: '', VIDEO_PROVIDER: '', ASR_PROVIDER: '', OPENAI_API_KEY: ''}, reuseExistingServer: false},
    {command: 'npm run dev -- --mode legacy-fixtures --port 5175', url: 'http://localhost:5175', env: {VITE_API_BASE_URL: 'http://localhost:8082'}, reuseExistingServer: false},
  ],
})
