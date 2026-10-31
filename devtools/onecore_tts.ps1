# 用 Windows OneCore 的中文语音（Kangkang 男 / Yaoyao 女）合成更自然的口播，存成 wav
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null = [Windows.Media.SpeechSynthesis.SpeechSynthesizer, Windows.Media, ContentType = WindowsRuntime]
$null = [Windows.Storage.Streams.IRandomAccessStream, Windows.Storage.Streams, ContentType = WindowsRuntime]

function Await($op, $type) {
  $asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
    $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'
  })[0]
  $task = $asTask.MakeGenericMethod($type).Invoke($null, @($op))
  $task.Wait(60000) | Out-Null
  return $task.Result
}

$texts = @{
  C = '这一个贴在 ACE Studio 边上的小工具，双击就能跑，不弹黑框，也不抢焦点。改歌词、搬参数、复制音高线，都变成一次点击。'
  A = '你看，这个工具其实就贴在 ACE 边上。双击就能跑，不弹黑框，也不抢焦点。改歌词、搬参数、复制音高线，都变成一次点击。'
  B = '今天咱们聊聊音频编辑。很多人以为，要调一条曲线，得反复地听、反复地改。其实只要把源和目标对齐，这事儿很快就完了。'
}

$outDir = Join-Path (Get-Location) 'docs\voice'
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

foreach ($voiceName in @('Kangkang', 'Yaoyao')) {
  foreach ($k in $texts.Keys) {
    $syn = New-Object Windows.Media.SpeechSynthesis.SpeechSynthesizer
    $v = [Windows.Media.SpeechSynthesis.SpeechSynthesizer]::AllVoices | Where-Object { $_.DisplayName -like "*$voiceName*" } | Select-Object -First 1
    if (-not $v) { "voice $voiceName 不存在"; continue }
    $syn.Voice = $v
    $syn.Options.SpeakingRate = 0.95
    $syn.Options.AudioPitch = 1.0
    $op = $syn.SynthesizeTextToStreamAsync($texts[$k])
    $stream = Await $op ([Windows.Media.SpeechSynthesis.SpeechSynthesisStream])
    $ms = New-Object System.IO.MemoryStream
    $netStream = [System.IO.WindowsRuntimeStreamExtensions]::AsStreamForRead($stream)
    $netStream.CopyTo($ms)
    $file = Join-Path $outDir ("onecore_{0}_{1}.wav" -f $voiceName.ToLower(), $k)
    [System.IO.File]::WriteAllBytes($file, $ms.ToArray())
    "{0} -> {1} bytes" -f $file, $ms.Length
    $ms.Dispose(); $stream.Dispose(); $syn.Dispose()
  }
}
