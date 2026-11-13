param([ValidateSet('file','postgres')][string]$Storage='file')
$ErrorActionPreference='Stop'
if ($env:CI -ne 'true') { throw 'Disposable CI setup only' }
$tools=Get-Content scripts/windows-tools.json -Raw | ConvertFrom-Json
$archive=Join-Path $env:RUNNER_TEMP 'studio-ffmpeg.zip'
if (-not (Test-Path -LiteralPath $archive)) { Invoke-WebRequest -Uri $tools.ffmpeg_url -OutFile $archive -TimeoutSec 180 }
if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $tools.ffmpeg_sha256) { throw 'FFmpeg checksum mismatch' }
$destination=Join-Path $env:RUNNER_TEMP 'studio-ffmpeg'
Expand-Archive -LiteralPath $archive -DestinationPath $destination -Force
$binary=Get-ChildItem $destination -Filter ffmpeg.exe -Recurse | Select-Object -First 1
if (-not $binary) { throw 'FFmpeg missing' }
$binary.DirectoryName | Out-File $env:GITHUB_PATH -Encoding utf8 -Append
# Required only by hostile-link tests, never by production service startup.
New-Item 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock' -Force | Out-Null
New-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock' -Name AllowDevelopmentWithoutDevLicense -Value 1 -PropertyType DWord -Force | Out-Null
if ($Storage -eq 'postgres') {
  $pgbin = Join-Path $env:PGBIN ''
  # Use a fresh per-run cluster rather than the image service's account/state.
  $data=Join-Path $env:RUNNER_TEMP 'studio-native-pg'
  if (Test-Path -LiteralPath $data) { throw 'Refusing to reuse PostgreSQL fixture data' }
  & (Join-Path $pgbin 'initdb.exe') -D $data --username=postgres --encoding=UTF8 --locale=C --auth=trust
  if ($LASTEXITCODE -ne 0) { throw 'Disposable PostgreSQL initialization failed' }
  & (Join-Path $pgbin 'pg_ctl.exe') -D $data -l (Join-Path $env:RUNNER_TEMP 'studio-native-pg.log') -o '-h 127.0.0.1 -p 55439' -w start
  if ($LASTEXITCODE -ne 0) { throw 'Disposable PostgreSQL startup failed' }
  foreach ($database in @('studio_windows_tests','studio_windows_runtime','studio_windows_scm')) {
    & (Join-Path $pgbin 'createdb.exe') -h 127.0.0.1 -p 55439 -U postgres $database
    if ($LASTEXITCODE -ne 0) { throw 'Disposable database creation failed' }
  }
  foreach ($name in @('STUDIO_POSTGRES_TEST_DSN','JOBQUEUE_TEST_DSN','STUDIO_BLOB_TEST_DSN','STUDIO_WORKER_TEST_DSN','STUDIO_AUDIT_TEST_DSN')) {
    "$name=postgresql://postgres@127.0.0.1:55439/studio_windows_tests?sslmode=disable" | Out-File $env:GITHUB_ENV -Encoding utf8 -Append
  }
}
