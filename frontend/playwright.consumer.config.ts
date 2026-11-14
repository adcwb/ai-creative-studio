import { defineConfig, devices } from '@playwright/test';
// The normal gateway-only UI must be checked independently of legacy fixture mode.
export default defineConfig({
    testDir: './e2e-consumer', outputDir: './test-results/consumer', workers: 1, fullyParallel: false, timeout: 30000,
    use: { baseURL: 'http://localhost:5178', locale: 'zh-CN', trace: 'retain-on-failure', channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL, launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } },
    projects: [{ name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1920, height: 1080 } } }, { name: 'mobile', use: { ...devices['iPhone 13'], defaultBrowserType: 'chromium', viewport: { width: 390, height: 844 } } }],
    webServer: process.env.PLAYWRIGHT_ROUTING_EXTERNAL ? undefined : [
        { command: 'node e2e-routing/fixtures/server.mjs', url: 'http://127.0.0.1:5189/__test__/health', reuseExistingServer: false },
        { command: 'npm run dev -- --port 5178', url: 'http://localhost:5178', env: { VITE_API_BASE_URL: 'http://127.0.0.1:5189' }, reuseExistingServer: false },
    ],
});
