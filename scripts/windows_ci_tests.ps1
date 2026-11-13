param([ValidateSet('file','postgres')][string]$Storage='file')
$ErrorActionPreference='Stop'
function Go-Checked {
  & go @args
  if ($LASTEXITCODE -ne 0) { throw 'Go validation failed' }
}
Push-Location backend
try {
  Go-Checked vet ./...
  foreach ($directory in Get-ChildItem cmd -Directory) { Go-Checked build -o ('bin/'+$directory.Name+'.exe') ('./cmd/'+$directory.Name) }
  Go-Checked test -count=1 -race ./internal/platform/... ./internal/persistence/... ./internal/modules/... ./cmd/...
  # Historical POSIX launcher fixtures remain fully covered on Linux. This set
  # executes portable provider contracts, authorization, storage and actual media.
  Go-Checked test -count=1 -race ./internal/application -run '^Test(Native|Bailian|Gateway|Auth|CORS|Deployment|RBAC|Upload|ModelRotation|RenderCheckpoint|Animatic|Postgres)'
} finally { Pop-Location }
$arguments=@('scripts/native_backend_check.py','--storage',$Storage)
if ($Storage -eq 'postgres') { $arguments+=@('--dsn','postgresql://postgres@127.0.0.1:55439/studio_windows_runtime?sslmode=disable') }
& python @arguments
if ($LASTEXITCODE -ne 0) { throw 'Native console acceptance failed' }
if ($Storage -eq 'postgres') { $arguments[-1]='postgresql://postgres@127.0.0.1:55439/studio_windows_scm?sslmode=disable' }
& python @arguments --scm
if ($LASTEXITCODE -ne 0) { throw 'Native SCM acceptance failed' }
