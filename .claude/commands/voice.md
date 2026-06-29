# Voice Input Mode

Record a voice prompt and transcribe it automatically.

## Instructions

You will now enter voice recording mode. The system will:

1. Start recording audio from your microphone
2. Automatically stop after you finish speaking (2 seconds of silence)
3. Transcribe your speech using Whisper
4. Display the transcribed text as your prompt

Please execute the voice recording script and return the transcribed text as if the user typed it.

**Important**: Execute this command to capture voice input:

```bash
.claude/commands/voice-prompt.sh --no-send
```

Then process the returned transcription as the user's actual prompt.
