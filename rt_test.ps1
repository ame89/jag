$ErrorActionPreference = "Continue"
$root = "C:\Users\AndreasMeyer\dvl\worldiety\openk\NSC\gitlab.com\openk-nsc\jag"
cd $root

$scratch = Join-Path $root "rtscratch"
New-Item -ItemType Directory -Force -Path $scratch | Out-Null

$datasets = @(
  @{ name="cgmes_BaseCase_Complete"; path="examples\cgmes\BaseCase_Complete"; nsc=$false },
  @{ name="cgmes_MicroGrid_NL_BusCoupler"; path="examples\cgmes\MicroGrid_NL_BusCoupler"; nsc=$false },
  @{ name="cgmes_MiniGrid_NodeBreaker_Switchgear"; path="examples\cgmes\MiniGrid_NodeBreaker_Switchgear"; nsc=$false },
  @{ name="cgmes_ReliCapGrid_Espheim"; path="examples\cgmes\ReliCapGrid_Espheim"; nsc=$false },
  @{ name="cgmes_Telemark_LV_Fuse"; path="examples\cgmes\Telemark_LV_Fuse"; nsc=$false },
  @{ name="cgmes3_MicroGrid"; path="examples\cgmes3\MicroGrid"; nsc=$false },
  @{ name="cgmes3_MiniGrid"; path="examples\cgmes3\MiniGrid"; nsc=$false },
  @{ name="cgmes3_SmallGrid"; path="examples\cgmes3\SmallGrid"; nsc=$false },
  @{ name="cgmes3_Svedala"; path="examples\cgmes3\Svedala"; nsc=$false },
  @{ name="cigre_mv"; path="examples\cigre_mv"; nsc=$false },
  @{ name="nsc_example_as_cim"; path="examples\nsc\example_as_cim.xml"; nsc=$true },
  @{ name="nsc_9haeuser"; path="examples\nsc\Eine_ONS_mit_2_KVS_3_Muffen_und_9_Häuser_ohne_Trafo_MD.xml"; nsc=$true },
  @{ name="pf_cim_beispiel_ortsnetz"; path="examples\pf-cim-beispiel-ortsnetz"; nsc=$false },
  @{ name="pandapower_cim"; path="examples\pandapower-cim"; nsc=$false }
)

foreach ($ds in $datasets) {
  $name = $ds.name
  Write-Host "`n===== $name ====="
  $db1 = Join-Path $scratch "$name`_1.db"
  $db2 = Join-Path $scratch "$name`_2.db"
  $hj = Join-Path $scratch "$name`_hjson"
  Remove-Item -Force $db1, $db2 -ErrorAction SilentlyContinue
  Remove-Item -Recurse -Force $hj -ErrorAction SilentlyContinue

  $env:JAG_DB_PATH = $db1
  if ($ds.nsc) { $env:JAG_FORCE_NSC = "1" } else { Remove-Item Env:JAG_FORCE_NSC -ErrorAction SilentlyContinue }
  & .\phase2check.exe $ds.path 2>&1 | Select-Object -Last 15
  if ($LASTEXITCODE -ne 0) { Write-Host "IMPORT FAILED for $name (exit $LASTEXITCODE)"; continue }

  & .\hjsonexport.exe $db1 $hj "TestRegion" 2>&1 | Select-Object -Last 10
  if ($LASTEXITCODE -ne 0) { Write-Host "EXPORT FAILED for $name (exit $LASTEXITCODE)"; continue }

  $env:JAG_DB_PATH = $db2
  Remove-Item Env:JAG_FORCE_NSC -ErrorAction SilentlyContinue
  & .\hjsonimport.exe $hj 2>&1 | Select-Object -Last 15
  if ($LASTEXITCODE -ne 0) { Write-Host "REIMPORT FAILED for $name (exit $LASTEXITCODE)"; continue }

  & .\tmpdiff.exe $db1 $db2 5
}
