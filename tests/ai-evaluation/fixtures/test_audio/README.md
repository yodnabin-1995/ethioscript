# Test Audio Fixtures

Reserved for Release 2 — STT (Speech-to-Text) evaluation.

## Format

Place audio files here alongside ground truth transcripts in
`../ground_truth/`.

## Supported formats (Release 2)

| Format | Notes |
|---|---|
| `.wav` | PCM, 16kHz mono preferred for Whisper |
| `.mp3` | Widely supported |
| `.mp4` | Audio extracted automatically |
| `.ogg` | Supported via ffmpeg |

## Naming convention

```
{language}_{condition}_{sequence}.{ext}
```

Examples:
```
amharic_clean_001.wav
amharic_noisy_001.wav
amharic_phone_001.mp3
tigrinya_broadcast_001.wav
```

## Conditions to evaluate (SRS 7.3 STT metrics)

- `clean` — studio quality, single speaker
- `noisy` — background noise present
- `phone` — telephone audio quality
- `broadcast` — radio/TV recording
- `multi` — multiple speakers

## WER/CER thresholds

Defined in `.env` or `pytest.ini` via:
```
STT_WER_THRESHOLD=0.25
STT_CER_THRESHOLD=0.15
```

*Release 2 stub — not yet active.*
