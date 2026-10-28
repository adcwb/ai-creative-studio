import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e-routing', testMatch: '*.spec.ts', workers: 1, fullyParallel: false, timeout: 30000,
  use: {baseURL: 'http://localhost:5178', locale: 'zh-CN', trace: 'retain-on-failure', channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL, launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}},
  projects: [{name: 'desktop', use: {...devices['Desktop Chrome']}}, {name: 'mobile', use: {...devices['iPhone 13'], defaultBrowserType: 'chromium'}}],
  webServer: process.env.PLAYWRIGHT_ROUTING_EXTERNAL ? undefined : [
    {command: 'node e2e-routing/fixtures/server.mjs', url: 'http://127.0.0.1:5189/__test__/health', reuseExistingServer: false},
    {command: 'npm run dev -- --port 5178', url: 'http://localhost:5178', env: {VITE_API_BASE_URL: 'http://127.0.0.1:5189'}, reuseExistingServer: false},
  ],
})
