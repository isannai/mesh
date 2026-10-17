# stamp-mesh-json.ps1 - copy a mesh.json into a package folder with "version" set to the build version.
#
# apps\<name>\mesh.json keeps a placeholder version (0.1.0) that nobody bumps, so a package built from it
# reports the wrong version in `isann mesh list`. The build stamps the real version into the COPY it packs.
# The source file is not touched.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File build\stamp-mesh-json.ps1 `
#       -Src apps\probe\mesh.json -Dst build\windows\out-mesh\probe\mesh.json -Version 0.1.41
#
# Exit code 1 (message on stderr) when the version is not version-looking, the file has no "version" key,
# or the result is not JSON that carries that version. The build scripts stop on it.
param(
  [Parameter(Mandatory = $true)][string]$Src,
  [Parameter(Mandatory = $true)][string]$Dst,
  [Parameter(Mandatory = $true)][string]$Version
)
$ErrorActionPreference = "Stop"
try {
  # The version goes into JSON text and into a regex replacement, so only version-looking text is allowed.
  if ($Version -notmatch '^[0-9A-Za-z][0-9A-Za-z.+-]*$') { throw "not a version: '$Version'" }

  $srcPath = (Resolve-Path -LiteralPath $Src).Path
  $text = [IO.File]::ReadAllText($srcPath)   # drops a BOM if there is one

  # The first "version" key is the top-level one (it sits in the header of the file).
  $re = New-Object System.Text.RegularExpressions.Regex('("version"\s*:\s*")[^"]*(")')
  if (-not $re.IsMatch($text)) { throw ('no "version" key in ' + $Src) }
  $stamped = $re.Replace($text, ('${1}' + $Version + '${2}'), 1)

  $parsed = $stamped | ConvertFrom-Json
  if ($parsed.version -ne $Version) { throw ('stamped version did not stick in ' + $Src) }

  # UTF-8 without a BOM, to the same line endings the source had.
  $dstPath = [IO.Path]::GetFullPath([IO.Path]::Combine((Get-Location).Path, $Dst))
  [IO.File]::WriteAllText($dstPath, $stamped, (New-Object System.Text.UTF8Encoding($false)))
} catch {
  [Console]::Error.WriteLine("stamp-mesh-json: " + $_.Exception.Message)
  exit 1
}
