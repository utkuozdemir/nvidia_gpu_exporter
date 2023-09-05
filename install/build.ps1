[CmdletBinding()]
Param (
    [Parameter(Mandatory = $true)]
    [String] $PathToExecutable,
    [Parameter(Mandatory = $true)]
    [String] $Version,
    [Parameter(Mandatory = $false)]
    [ValidateSet("amd64", "386", "amd64_v1")]
    [String] $Arch = "amd64"
)
$ErrorActionPreference = "Stop"

# Get absolute path to executable before switching directories
$PathToExecutable = Resolve-Path $PathToExecutable
# Set working dir to this directory, reset previous on exit
Push-Location $PSScriptRoot
Trap {
    # Reset working dir on error
    Pop-Location
}

if ($PSVersionTable.PSVersion.Major -lt 5) {
    Write-Error "Powershell version 5 required"
    exit 1
}

$wc = New-Object System.Net.WebClient
function Get-FileIfNotExists {
    Param (
        $Url,
        $Destination
    )
    if (-not (Test-Path $Destination)) {
        Write-Verbose "Downloading $Url"
        $wc.DownloadFile($Url, $Destination)
    }
    else {
        Write-Verbose "${Destination} already exists. Skipping."
    }
}

$sourceDir = mkdir -Force Source
mkdir -Force Work, Output | Out-Null

Write-Verbose "Downloading WiX..."
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Get-FileIfNotExists "https://github.com/wixtoolset/wix3/releases/download/wix311rtm/wix311-binaries.zip" "$sourceDir\wix-binaries.zip"
mkdir -Force WiX | Out-Null
Expand-Archive -Path "${sourceDir}\wix-binaries.zip" -DestinationPath WiX -Force

Copy-Item -Force $PathToExecutable Work/nvidia_gpu_exporter.exe

Write-Verbose "Creating nvidia_gpu_exporter_${Version}_${Arch}.msi"
$wixArch = @{"amd64" = "x64"; "amd64_v1" = "x64"; "386" = "x86"}[$Arch]
$wixOpts = "-ext WixFirewallExtension -ext WixUtilExtension"
Invoke-Expression "WiX\candle.exe -nologo -arch $wixArch $wixOpts -out Work\nvidia_gpu_exporter.wixobj -dVersion=`"$Version`" nvidia_gpu_exporter.wxs"
Invoke-Expression "WiX\light.exe -nologo -spdb $wixOpts -out `"Output\nvidia_gpu_exporter_${Version}_${Arch}.msi`" Work\nvidia_gpu_exporter.wixobj"

Write-Verbose "Done!"
Pop-Location
