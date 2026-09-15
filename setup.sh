#!/bin/bash
# setup.sh - Verify notification plugin installation

set -e

echo "=========================================="
echo " Agent Notifications Plugin - Setup"
echo "=========================================="
echo ""

# Check if wrapper script exists
if [ ! -f "bin/claude-notifications" ]; then
    echo "❌ Error: bin/claude-notifications wrapper not found"
    echo ""
    echo "This file should be included in the repository."
    exit 1
fi

# Check if installer exists
if [ ! -f "bin/install.sh" ]; then
    echo "❌ Error: bin/install.sh installer not found"
    echo ""
    echo "This file should be included in the repository."
    exit 1
fi

# Make scripts executable
chmod +x bin/claude-notifications
chmod +x bin/install.sh

echo "✓ Plugin scripts verified"
echo ""
echo "=========================================="
echo " Setup Complete!"
echo "=========================================="
echo ""
echo "Next steps:"
echo ""
echo "Run these commands inside Claude Code:"
echo ""
echo "1. Add marketplace:"
echo "   /plugin marketplace add 777genius/agent-notifications"
echo ""
echo "2. Install plugin:"
echo "   /plugin install claude-notifications-go@claude-notifications-go"
echo ""
echo "3. Restart Claude Code for hooks to take effect"
echo ""
echo "4. Download the binary for your platform:"
echo "   /claude-notifications-go:init"
echo ""
echo "5. Configure notification sounds (optional):"
echo "   /claude-notifications-go:settings"
echo ""
echo "   This will let you:"
echo "   - Preview and choose notification sounds"
echo "   - Configure volume"
echo "   - Set up webhooks (optional)"
echo ""
echo "Note: The binary will be downloaded automatically when you"
echo "      run /claude-notifications-go:init for the first time."
echo ""
