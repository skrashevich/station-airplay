# ALAC stereo fixture

These files contain generated audio, not a recording. The fixture covers only
signed 16-bit stereo at 44100 Hz, 352 samples per channel.

- `alac-stereo16-44100.pcm`: interleaved signed 16-bit little-endian PCM.
- `alac-stereo16-44100.packet`: one compressed ALAC packet encoded by FFmpeg
  9.0.1. Its explicit sample count is 352; the adapter does not use an MP4 cookie.

Regenerate the input:

```python
import struct
from pathlib import Path
Path("alac-stereo16-44100.pcm").write_bytes(b"".join(
    struct.pack("<hh", (i * 97 % 32768) - 16384, 12000 - i * 53)
    for i in range(352)))
```

Encode and independently verify losslessness:

```sh
ffmpeg -f s16le -ar 44100 -ac 2 -i alac-stereo16-44100.pcm -c:a alac fixture.m4a
ffmpeg -i fixture.m4a -f s16le decoded.pcm
cmp alac-stereo16-44100.pcm decoded.pcm
ffprobe -v error -show_packets -show_data -of json fixture.m4a > packets.json
```

The single packet's `data` hex dump in `packets.json`, without byte offsets or
the ASCII column, is `alac-stereo16-44100.packet` (214 bytes). The PCM reference
is the original formula output and was compared byte for byte with FFmpeg's
independent decoder output. FFmpeg is required only to regenerate fixtures;
regular Go tests and the receiver do not execute or link it.
