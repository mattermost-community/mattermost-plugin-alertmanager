#!/bin/bash
# Voice to Input - Simulates typing the transcribed text
# This allows voice input to work like keyboard input in any application

set -e

RECORDING_DIR="${HOME}/.voice-to-claude"
AUDIO_FILE="${RECORDING_DIR}/recording.wav"
TRANSCRIPT_FILE="${RECORDING_DIR}/transcript.txt"
WHISPER_MODEL="${WHISPER_MODEL:-small}"

mkdir -p "$RECORDING_DIR"

# Record audio with silence detection
echo "🎤 Recording... (speak now, will auto-stop after silence)"
if command -v rec &> /dev/null; then
    rec -r 16000 -c 1 "$AUDIO_FILE" silence 1 0.1 1% 1 2.0 3% 2>/dev/null
else
    echo "❌ Error: Install sox with: brew install sox"
    exit 1
fi

echo "🔄 Transcribing..."
whisper "$AUDIO_FILE" \
    --model "$WHISPER_MODEL" \
    --language en \
    --task transcribe \
    --output_format txt \
    --output_dir "$RECORDING_DIR" \
    --fp16 False \
    > /dev/null 2>&1

mv "${RECORDING_DIR}/recording.txt" "$TRANSCRIPT_FILE" 2>/dev/null
transcript=$(cat "$TRANSCRIPT_FILE" | tr -d '\n')

echo "✓ Transcribed: $transcript"

# Copy to clipboard for easy pasting
if command -v pbcopy &> /dev/null; then
    echo -n "$transcript" | pbcopy
    echo "✓ Copied to clipboard - paste with Cmd+V"

    # Optional: Auto-paste using AppleScript (requires accessibility permissions)
    # Uncomment the next 3 lines to enable auto-paste:
    sleep 0.5
    osascript -e 'tell application "System Events" to keystroke "v" using command down'
    echo "✓ Auto-pasted"
fi
