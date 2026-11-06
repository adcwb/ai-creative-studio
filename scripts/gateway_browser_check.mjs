#!/usr/bin/env node
// Each default spec/device suite starts its own temporary backend. Do not weaken production
// authentication rate limits to accommodate many synthetic registrations.
import {spawnSync} from 'node:child_process'
import {fileURLToPath} from 'node:url'
import {readdirSync} from 'node:fs'
import {join} from 'node:path'
const frontend=fileURLToPath(new URL('../frontend/',import.meta.url))
const cli=fileURLToPath(new URL('../frontend/node_modules/@playwright/test/cli.js',import.meta.url))
const args=process.argv.slice(2)
if(args.some(a=>a==='--project'||a.startsWith('--project='))){
 console.error('This command isolates both device projects. Use Playwright directly to select one project.')
 process.exit(2)
}
// All files are independent account fixtures. A growing suite must not consume
// the shared IP registration allowance or depend on IPv4/IPv6 distribution.
// Explicit selectors retain normal Playwright filtering for focused runs.
const groups=args.length?[args]:readdirSync(join(frontend,'e2e-gateway')).filter(file=>file.endsWith('.spec.ts')).sort().map(file=>[file])
if(!groups.length){console.error('No gateway browser specs found.');process.exit(1)}
for(const project of ['desktop','mobile']){
 for(const group of groups){
  console.log(`Gateway browser suite: ${project} / ${group.join(' ')}, with a fresh temporary backend.`)
  const result=spawnSync(process.execPath,[cli,'test','--config','playwright.gateway.config.ts',`--project=${project}`,...group],{cwd:frontend,stdio:'inherit'})
  if(result.error){console.error(result.error.message);process.exit(1)}
  if(result.status!==0)process.exit(result.status??1)
 }
}
