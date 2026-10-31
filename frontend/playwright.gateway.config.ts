import {isolatedServerEnv} from './playwright-server-env'
import {defineConfig,devices} from '@playwright/test'
import {mkdtempSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
export default defineConfig({testDir:'./e2e-gateway',workers:1,fullyParallel:false,timeout:30000,
 use:{locale:'zh-CN',baseURL:'http://localhost:5179',trace:'retain-on-failure',channel:process.env.PLAYWRIGHT_BROWSER_CHANNEL,launchOptions:{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}},
 projects:[{name:'desktop',use:{...devices['Desktop Chrome']}},{name:'mobile',use:{...devices['iPhone 13'],defaultBrowserType:'chromium'}}],
 webServer:[
 {command:'node e2e-gateway/upstream.mjs',url:'http://127.0.0.1:5193/__health',reuseExistingServer:false},
 {command:'../backend/bin/server',url:'http://127.0.0.1:8084/healthz',reuseExistingServer:false,env:{...isolatedServerEnv,AUTH_MODE:'secure',AUTH_ALLOW_REGISTRATION:'true',AUTH_COOKIE_SECURE:'false',HTTP_ADDR:'127.0.0.1:8084',FRONTEND_ORIGIN:'http://localhost:5179',DATA_DIR:mkdtempSync(join(tmpdir(),'studio-gateway-ui-')),IMAGE_PROVIDER:'mock-svg',TEXT_PROVIDER:'mock-text',MODEL_CONFIG_KEK_BASE64:Buffer.alloc(32,71).toString('base64'),MODEL_GATEWAY_ALLOWED_HOSTS:'127.0.0.1:5193'}},
 {command:'npm run dev -- --port 5179',url:'http://localhost:5179',reuseExistingServer:false,env:{VITE_API_BASE_URL:'http://localhost:8084'}}]})
