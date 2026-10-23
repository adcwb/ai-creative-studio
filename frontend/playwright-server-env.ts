// Playwright merges webServer.env into the parent environment. Clear names without
// reading values, then explicitly allow PATH and each test's synthetic settings.
// This also blocks future OIDC, KEK, S3, local command and provider variables.
export const isolatedServerEnv: Record<string, string> = {
  ...Object.fromEntries(Object.keys(process.env).map(name => [name, ''])),
  PATH: process.env.PATH ?? '',
}
