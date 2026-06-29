# Claude Account Switcher
# Uses CLAUDE_PERSONAL_KEY and CLAUDE_ENTERPRISE_KEY from .zshrc
#
# Two modes:
#   cs personal   - Apps use personal API key via $ANTHROPIC_API_KEY
#                   Claude Code uses Max subscription (via wrapper)
#
#   cs enterprise - Apps use enterprise API key via $ANTHROPIC_API_KEY
#                   Claude Code uses enterprise API key
#
# Usage:
#   cs personal   # Switch to personal mode
#   claude        # Run Claude Code (wrapper respects mode)
#
#   cs enterprise # Switch to enterprise mode
#   claude / ce   # Run Claude Code with enterprise key
#
# Apps read: $ANTHROPIC_API_KEY (always set to current mode's key)

# Enterprise mode uses a separate home directory to bypass OAuth
# Use actual user home, not $HOME which may already be enterprise home
CLAUDE_ENTERPRISE_HOME="$HOME/.claude-enterprise-home"

cs() {
    case "$1" in
        enterprise|ent|e)
            export ANTHROPIC_API_KEY="$CLAUDE_ENTERPRISE_KEY"
            export CLAUDE_MODE="enterprise"
            # Gemini/OpenAI keys from .zshrc (for /multi-review)
            export OPENAI_API_KEY="$OPENAI_ENTERPRISE_KEY"
            export GOOGLE_API_KEY="$GEMINI_ENTERPRISE_KEY"
            export GEMINI_API_KEY="$GEMINI_ENTERPRISE_KEY"
            echo "✅ ENTERPRISE mode"
            echo "   Apps/Scripts: Enterprise API key via \$ANTHROPIC_API_KEY"
            echo "   Claude Code: Enterprise API key (use 'claude' or 'ce' command)"
            echo "   Gemini/OpenAI: Enterprise keys set for /multi-review"
            ;;
        max|personal|p)
            # Set ANTHROPIC_API_KEY for apps to use personal key
            export ANTHROPIC_API_KEY="$CLAUDE_PERSONAL_KEY"
            export CLAUDE_MODE="personal"
            # Gemini/OpenAI keys from .zshrc (for /multi-review)
            export OPENAI_API_KEY="$OPENAI_PERSONAL_KEY"
            export GOOGLE_API_KEY="$GEMINI_PERSONAL_KEY"
            export GEMINI_API_KEY="$GEMINI_PERSONAL_KEY"
            echo "✅ PERSONAL mode"
            echo "   Apps/Scripts: Personal API key via \$ANTHROPIC_API_KEY"
            echo "   Claude Code: Max subscription (use 'claude' wrapper command)"
            echo "   Gemini/OpenAI: Personal keys set for /multi-review"
            ;;
        status|s)
            echo "Current mode: ${CLAUDE_MODE:-unknown}"
            echo ""
            if [[ "$CLAUDE_MODE" == "enterprise" ]]; then
                echo "Apps/Scripts: \$ANTHROPIC_API_KEY = ${ANTHROPIC_API_KEY:0:20}... (enterprise)"
                echo "Claude Code: Uses enterprise API key"
                echo "Gemini:      \$GEMINI_API_KEY = ${GEMINI_API_KEY:0:20}..."
                echo "OpenAI:      \$OPENAI_API_KEY = ${OPENAI_API_KEY:0:20}... (enterprise)"
            elif [[ "$CLAUDE_MODE" == "personal" ]]; then
                echo "Apps/Scripts: \$ANTHROPIC_API_KEY = ${ANTHROPIC_API_KEY:0:20}... (personal)"
                echo "Claude Code: Uses Max subscription (via wrapper)"
                echo "Gemini:      \$GEMINI_API_KEY = ${GEMINI_API_KEY:0:20}..."
                echo "OpenAI:      \$OPENAI_API_KEY = ${OPENAI_API_KEY:0:20}... (personal)"
            else
                echo "⚠️  No mode set. Run 'cs personal' or 'cs enterprise'"
            fi
            ;;
        *)
            echo "Usage: cs {personal|enterprise|status}"
            echo ""
            echo "Modes:"
            echo "  cs personal    - Personal mode"
            echo "                   Apps: Use personal API key (\$ANTHROPIC_API_KEY)"
            echo "                   Claude Code: Max subscription (browser auth)"
            echo ""
            echo "  cs enterprise  - Enterprise mode"
            echo "                   Apps: Use enterprise API key (\$ANTHROPIC_API_KEY)"
            echo "                   Claude Code: Enterprise API key"
            echo ""
            echo "  cs status      - Show current mode and key info"
            echo ""
            echo "Commands: 'claude' (mode-aware wrapper), 'ce' (enterprise direct)"
            ;;
    esac
}

# Claude Code wrapper - respects mode settings
claude() {
    if [[ "$CLAUDE_MODE" == "personal" ]]; then
        # In personal mode: unset API key so Claude Code uses Max subscription
        ANTHROPIC_API_KEY="" command claude "$@"
    elif [[ "$CLAUDE_MODE" == "enterprise" ]]; then
        # In enterprise mode: use separate home to bypass Max OAuth session
        # Setup enterprise home if first run
        if [[ ! -d "$CLAUDE_ENTERPRISE_HOME/.claude" ]]; then
            echo "Setting up enterprise config (first run)..."
            mkdir -p "$CLAUDE_ENTERPRISE_HOME/.claude"
            # Symlink shared configs from real home
            ln -sf "$HOME/.claude/settings.json" "$CLAUDE_ENTERPRISE_HOME/.claude/settings.json" 2>/dev/null
            ln -sf "$HOME/.claude/settings.local.json" "$CLAUDE_ENTERPRISE_HOME/.claude/settings.local.json" 2>/dev/null
            ln -sf "$HOME/.claude/CLAUDE.md" "$CLAUDE_ENTERPRISE_HOME/.claude/CLAUDE.md" 2>/dev/null
        fi

        # Launch with enterprise home (bypasses Max OAuth, uses API key)
        # Add enterprise home's .local/bin to PATH so Claude Code doesn't complain
        HOME="$CLAUDE_ENTERPRISE_HOME" \
        PATH="$CLAUDE_ENTERPRISE_HOME/.local/bin:$PATH" \
        ANTHROPIC_API_KEY="$CLAUDE_ENTERPRISE_KEY" \
        command claude "$@"
    else
        # No mode set, use default
        command claude "$@"
    fi
}

# Enterprise claude - uses separate home directory so no OAuth session exists
ce() {
    if [[ -z "$CLAUDE_ENTERPRISE_KEY" ]]; then
        echo "❌ CLAUDE_ENTERPRISE_KEY not set in .zshrc"
        return 1
    fi

    # Setup enterprise home if first run - use symlinks to share config
    if [[ ! -d "$CLAUDE_ENTERPRISE_HOME/.claude" ]]; then
        echo "Setting up enterprise config (first run)..."
        mkdir -p "$CLAUDE_ENTERPRISE_HOME/.claude"
        # Symlink shared configs from real home
        ln -sf "$HOME/.claude/settings.json" "$CLAUDE_ENTERPRISE_HOME/.claude/settings.json"
        ln -sf "$HOME/.claude/settings.local.json" "$CLAUDE_ENTERPRISE_HOME/.claude/settings.local.json" 2>/dev/null
        ln -sf "$HOME/.claude/CLAUDE.md" "$CLAUDE_ENTERPRISE_HOME/.claude/CLAUDE.md"
    fi

    # Set mode
    export CLAUDE_MODE="enterprise"

    # Launch with enterprise home (bypasses Max OAuth, uses API key)
    HOME="$CLAUDE_ENTERPRISE_HOME" \
    PATH="$CLAUDE_ENTERPRISE_HOME/.local/bin:$PATH" \
    ANTHROPIC_API_KEY="$CLAUDE_ENTERPRISE_KEY" \
    command claude "$@"
}
