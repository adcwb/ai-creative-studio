#!/usr/bin/env node
// Each device suite starts its own temporary backend. Do not weaken production
// authentication rate limits to accommodate many synthetic registrations.
import {spawnSync} from 'node:child_process'
import {fileURLToPath} from 'node:url'
const frontend=fileURLToPath(new URL('../frontend/',import.meta.url))
const cli=fileURLToPath(new URL('../frontend/node_modules/@playwright/test/cli.js',import.meta.url))
const args=process.argv.slice(2)
if(args.some(a=>a==='--project'||a.startsWith('--project='))){
 console.error('This command isolates both device projects. Use Playwright directly to select one project.')
 process.exit(2)
}
for(const project of ['desktop','mobile']){
 console.log(`Gateway browser suite: ${project}, with a fresh temporary backend.`)
 const result=spawnSync(process.execPath,[cli,'test','--config','playwright.gateway.config.ts',`--project=${project}`,...args],{cwd:frontend,stdio:'inherit'})
 if(result.error){console.error(result.error.message);process.exit(1)}
 if(result.status!==0)process.exit(result.status??1)
}
