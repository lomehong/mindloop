# 生成 mindloop 应用图标源图：1024x1024，深色方底 + 蓝色圆点
# （与 web 导航栏 logo 同款视觉）。输出 desktop/app-icon.png。
$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Drawing

$out = Join-Path $PSScriptRoot "app-icon.png"
$size = 1024

$bmp = New-Object System.Drawing.Bitmap($size, $size)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
$g.Clear([System.Drawing.Color]::Transparent)

# 深色方底（内缩一圈留透明边，深浅色模式都不突兀）
$margin = 32
$side = $size - $margin * 2
$bg = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(255, 22, 24, 29))
$g.FillRectangle($bg, $margin, $margin, $side, $side)

# 蓝色圆点（logo-dot 同款色 #3b82f6），外圈半透明光环
$center = [int]($size / 2)
$halo = [int]($size * 0.28)
$haloBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(70, 59, 130, 246))
$g.FillEllipse($haloBrush, $center - $halo, $center - $halo, $halo * 2, $halo * 2)

$dot = [int]($size * 0.18)
$dotBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(255, 59, 130, 246))
$g.FillEllipse($dotBrush, $center - $dot, $center - $dot, $dot * 2, $dot * 2)

$g.Dispose()
$bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()
Write-Host "icon written: $out"
