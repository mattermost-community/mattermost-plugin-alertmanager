#!/bin/bash
# Voice Prompt for Claude Code
# Records audio, transcribes with Whisper, and sends directly to Claude Code

set -e

# Configuration
RECORDING_DIR="${HOME}/.voice-to-claude"
AUDIO_FILE="${RECORDING_DIR}/recording.wav"
TRANSCRIPT_FILE="${RECORDING_DIR}/transcript.txt"
WHISPER_MODEL="${WHISPER_MODEL:-base}"
RECORDING_DURATION="${RECORDING_DURATION:-60}"  # Max recording duration in seconds

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# Create recording directory if it doesn't exist
mkdir -p "$RECORDING_DIR"

# Check dependencies
check_dependencies() {
    if ! command -v whisper &> /dev/null; then
        echo -e "${RED}Error: whisper is not installed${NC}"
        echo "Install with: pip install openai-whisper"
        exit 1
    fi

    if ! command -v claude &> /dev/null; then
        echo -e "${RED}Error: claude CLI is not installed${NC}"
        echo "Install from: https://claude.com/claude-code"
        exit 1
    fi
}

# Function to record audio on macOS with voice activity detection
record_audio_macos() {
    echo -e "${BLUE}🎤 Recording... (will auto-stop after silence or max ${RECORDING_DURATION}s)${NC}"
    echo -e "${YELLOW}Speak your prompt now...${NC}"

    # Check if SoX is installed (better quality recording with silence detection)
    if command -v rec &> /dev/null; then
        # Record with silence detection: stops after 2 seconds of silence
        rec -r 16000 -c 1 "$AUDIO_FILE" silence 1 0.1 1% 1 2.0 3% &
        REC_PID=$!

        # Kill after max duration
        (sleep $RECORDING_DURATION && kill $REC_PID 2>/dev/null) &
        TIMEOUT_PID=$!

        # Wait for recording to complete
        wait $REC_PID 2>/dev/null
        kill $TIMEOUT_PID 2>/dev/null || true

    elif command -v ffmpeg &> /dev/null; then
        # Fallback to ffmpeg
        ffmpeg -f avfoundation -i ":0" -t $RECORDING_DURATION -ar 16000 -ac 1 "$AUDIO_FILE" 2>/dev/null
    else
        echo -e "${RED}Error: No audio recording tool found${NC}"
        echo "Install SoX (recommended): brew install sox"
        echo "Or install ffmpeg: brew install ffmpeg"
        exit 1
    fi

    echo -e "${GREEN}✓ Recording complete${NC}"
}

# Function to transcribe audio with Whisper
transcribe_audio() {
    echo -e "${BLUE}🔄 Transcribing...${NC}"

    # Run whisper and extract text output
    whisper "$AUDIO_FILE" \
        --model "$WHISPER_MODEL" \
        --language en \
        --task transcribe \
        --output_format txt \
        --output_dir "$RECORDING_DIR" \
        --fp16 False \
        > /dev/null 2>&1

    # The output file will be recording.txt
    mv "${RECORDING_DIR}/recording.txt" "$TRANSCRIPT_FILE" 2>/dev/null || true

    if [ ! -f "$TRANSCRIPT_FILE" ]; then
        echo -e "${RED}Error: Transcription failed${NC}"
        exit 1
    fi

    # Clean up the transcript (remove extra whitespace)
    sed -i.bak 's/^[[:space:]]*//;s/[[:space:]]*$//' "$TRANSCRIPT_FILE"
    rm "${TRANSCRIPT_FILE}.bak" 2>/dev/null || true

    echo -e "${GREEN}✓ Transcription complete${NC}"
}

# Function to send to Claude Code
send_to_claude() {
    local transcript=$(cat "$TRANSCRIPT_FILE")

    # Show the transcription
    echo -e "${BLUE}📝 Your prompt:${NC}"
    echo -e "${YELLOW}${transcript}${NC}"
    echo ""

    # Check if Claude Code is already running in a session
    if [ "${CLAUDE_SESSION:-}" = "active" ]; then
        # Just output the transcript if we're in an active session
        echo "$transcript"
    else
        # Send to Claude Code with --continue to add to current conversation
        echo -e "${BLUE}🚀 Sending to Claude Code...${NC}"
        echo "$transcript" | claude --continue
    fi
}

# Main execution
main() {
    # Parse command line arguments
    case "${1:-}" in
        --help|-h)
            echo "Voice Prompt for Claude Code"
            echo ""
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --help, -h          Show this help message"
            echo "  --file FILE         Transcribe existing audio file"
            echo "  --model MODEL       Whisper model (tiny|base|small|medium|large)"
            echo "  --duration SEC      Max recording duration in seconds (default: 60)"
            echo "  --no-send           Only transcribe, don't send to Claude"
            echo ""
            echo "Environment Variables:"
            echo "  WHISPER_MODEL       Default Whisper model (default: base)"
            echo "  RECORDING_DURATION  Max recording duration (default: 60)"
            echo ""
            echo "Examples:"
            echo "  $0                           # Record and send to Claude"
            echo "  $0 --model small             # Use better model"
            echo "  $0 --file audio.wav          # Transcribe existing file"
            exit 0
            ;;
        --file)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}Error: --file requires an argument${NC}"
                exit 1
            fi
            AUDIO_FILE="$2"
            shift 2
            ;;
        --model)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}Error: --model requires an argument${NC}"
                exit 1
            fi
            WHISPER_MODEL="$2"
            shift 2
            ;;
        --duration)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}Error: --duration requires an argument${NC}"
                exit 1
            fi
            RECORDING_DURATION="$2"
            shift 2
            ;;
        --no-send)
            NO_SEND=true
            shift
            ;;
    esac

    check_dependencies

    # Record audio if not using existing file
    if [ "$AUDIO_FILE" = "${RECORDING_DIR}/recording.wav" ]; then
        record_audio_macos
    fi

    # Transcribe audio
    transcribe_audio

    # Send to Claude unless --no-send
    if [ "${NO_SEND:-false}" != "true" ]; then
        send_to_claude
    else
        cat "$TRANSCRIPT_FILE"
    fi
}

# Run main function
main "$@"
