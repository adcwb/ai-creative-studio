param([ValidateSet('start','remove')][string]$Action,[ValidateSet('CreativeStudioAPI','CreativeStudioWorker')][string]$Name,[string]$Executable,[string]$EnvironmentFile)
$ErrorActionPreference = 'Stop'
if ($env:CI -ne 'true') { throw 'SCM fixture is restricted to disposable CI' }
if (-not $env:GITHUB_RUN_ID) { throw 'GitHub fixture ownership required' }
$marker = $env:GITHUB_RUN_ID + '/' + $env:GITHUB_RUN_ATTEMPT
$registry = 'HKLM:\SYSTEM\CurrentControlSet\Services\' + $Name
$existing = Get-Service -Name $Name -ErrorAction SilentlyContinue
if ($Action -eq 'remove') {
  if ($existing) {
    $owner = Get-ItemPropertyValue -LiteralPath $registry -Name StudioCIFixture -ErrorAction SilentlyContinue
    if ($owner -ne $marker) { throw 'Refusing to remove an unowned service' }
    Stop-Service -Name $Name -Force
    $existing.WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))
    & sc.exe delete $Name | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'SCM delete failed' }
    $existing.Dispose()
  }
  return
}
if ($existing) { throw 'Refusing to overwrite an existing service' }
if (-not [IO.Path]::IsPathRooted($Executable)) { throw 'Absolute executable required' }
$values = Get-Content -LiteralPath $EnvironmentFile -Raw -Encoding UTF8 | ConvertFrom-Json
$environment = @($values.PSObject.Properties | ForEach-Object { $_.Name + '=' + [string]$_.Value })
New-Service -Name $Name -BinaryPathName ('"'+$Executable+'"') -StartupType Manual | Out-Null
New-ItemProperty -LiteralPath $registry -Name StudioCIFixture -PropertyType String -Value $marker -Force | Out-Null
New-ItemProperty -LiteralPath $registry -Name Environment -PropertyType MultiString -Value $environment -Force | Out-Null
Start-Service -Name $Name
(Get-Service -Name $Name).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
