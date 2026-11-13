param([ValidateSet('file','postgres')][string]$Storage='file')
$ErrorActionPreference='Stop'
function Go-Checked {
  & go @args
  if ($LASTEXITCODE -ne 0) { throw 'Go validation failed' }
}
Push-Location backend
try {
  Write-Host "Windows native vet and command builds"
  Go-Checked vet ./...
  foreach ($directory in Get-ChildItem cmd -Directory) { Go-Checked build -o ('bin/'+$directory.Name+'.exe') ('./cmd/'+$directory.Name) }
} finally { Pop-Location }
$arguments=@('scripts/native_backend_check.py','--storage',$Storage)
if ($Storage -eq 'postgres') { $arguments+=@('--dsn','postgresql://postgres@127.0.0.1:55439/studio_windows_runtime?sslmode=disable') }
Write-Host "Windows native console acceptance"
& python @arguments
if ($LASTEXITCODE -ne 0) { throw 'Native console acceptance failed' }
if ($Storage -eq 'postgres') { $arguments[-1]='postgresql://postgres@127.0.0.1:55439/studio_windows_scm?sslmode=disable' }
Write-Host "Windows native SCM acceptance"
& python @arguments --scm
if ($LASTEXITCODE -ne 0) { throw 'Native SCM acceptance failed' }

# Cheap vertical runtime acceptance precedes the longer suites; retain all coverage.
Write-Host "Windows native foundation and application race suites"
Push-Location backend
try {
  Go-Checked test -count=1 -race ./internal/platform/... ./internal/persistence/... ./internal/modules/... ./cmd/...
  # Historical POSIX launcher fixtures remain fully covered on Linux. This set
  # executes portable provider contracts, authorization, storage and actual media.
  Go-Checked test -count=1 -race ./internal/application -run '^Test(Native|Bailian|Gateway|Auth|CORS|Deployment|RBAC|Upload|ResumableUpload|FileSeries|Series|Media|S3Multipart|Encrypted|ModelRotation|RenderCheckpoint|Animatic|Postgres)'
} finally { Pop-Location }
