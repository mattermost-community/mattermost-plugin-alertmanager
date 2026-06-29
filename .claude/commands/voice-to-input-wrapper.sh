#!/bin/bash
# Wrapper for Automator - sets up environment properly

# Set up PATH to include Homebrew and Python paths
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:$HOME/.local/bin:$PATH"

# Set up Python path for whisper if installed via pip
if [ -d "$HOME/Library/Python/3.9/bin" ]; then
    export PATH="$HOME/Library/Python/3.9/bin:$PATH"
fi
if [ -d "$HOME/Library/Python/3.11/bin" ]; then
    export PATH="$HOME/Library/Python/3.11/bin:$PATH"
fi
if [ -d "$HOME/Library/Python/3.12/bin" ]; then
    export PATH="$HOME/Library/Python/3.12/bin:$PATH"
fi

# Run the actual script with full path
exec "$HOME/.claude/commands/voice-to-input.sh"
