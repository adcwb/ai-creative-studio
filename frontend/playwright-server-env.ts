// Playwright merges webServer.env into the parent environment. Clear names without
// reading secret values, then allow PATH, native Windows paths and synthetic settings.
// This also blocks future OIDC, KEK, S3, local command and provider variables.
export const isolatedServerEnv: Record<string, string> = {
  ...Object.fromEntries(Object.keys(process.env).map(name => [name, ''])),
  PATH: process.env.PATH ?? '',
  ...Object.fromEntries(Object.keys(process.env).filter(name => ['SYSTEMROOT', 'WINDIR', 'TEMP', 'TMP'].includes(name.toUpperCase())).map(name => [name, process.env[name] ?? ''])),
}
