# 测试音频

`sample-1s.wav`：1 秒 440 Hz 正弦波，16 kHz / 单声道 / 16-bit PCM，仅用于冒烟测试，不含人声。

重新生成：

```bash
ffmpeg -f lavfi -i "sine=frequency=440:duration=1" \
  -ar 16000 -ac 1 -c:a pcm_s16le sample-1s.wav
```

不要提交真实录音或包含个人信息的声音样本。
