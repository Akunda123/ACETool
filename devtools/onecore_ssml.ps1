# OneCore 中文语音 + SSML（分句变速、加停顿）→ 更像人说话的口播
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

$ssml = @'
<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xml:lang="zh-CN">
  <voice name="Microsoft Kangkang">
    <prosody rate="0.92" pitch="-2%">你看啊，这个工具，其实就贴在 ACE 边上。</prosody>
    <break time="260ms"/>
    <prosody rate="1.0" pitch="0%">双击就能跑，不弹黑框，也不抢焦点。</prosody>
    <break time="180ms"/>
    <prosody rate="0.95" pitch="+1%">改歌词、搬参数、复制音高线——都变成一次点击。</prosody>
  </voice>
</speak>
'@

$ssmlY = $ssml -replace 'Microsoft Kangkang', 'Microsoft Yaoyao'

$outDir = Join-Path (Get-Location) 'docs\voice'
foreach ($pair in @(@('kangkang', $ssml), @('yaoyao', $ssmlY))) {
  $syn = New-Object Windows.Media.SpeechSynthesis.SpeechSynthesizer
  $op = $syn.SynthesizeSsmlToStreamAsync($pair[1])
  $stream = Await $op ([Windows.Media.SpeechSynthesis.SpeechSynthesisStream])
  $ms = New-Object System.IO.MemoryStream
  ([System.IO.WindowsRuntimeStreamExtensions]::AsStreamForRead($stream)).CopyTo($ms)
  $file = Join-Path $outDir ("natural_{0}.wav" -f $pair[0])
  [System.IO.File]::WriteAllBytes($file, $ms.ToArray())
  "{0} -> {1} bytes" -f $file, $ms.Length
  $ms.Dispose(); $stream.Dispose(); $syn.Dispose()
}
