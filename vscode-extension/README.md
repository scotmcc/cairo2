# Cairo VS Code Extension

**The best of both worlds**: Use Cairo's powerful TUI from your terminal, or interact seamlessly from VS Code.

## 🎯 Dual-Mode Approach

### Option 1: **VS Code Chat Panel** (Recommended)
- Open the dedicated Cairo sidebar
- Full chat interface with streaming responses
- Real-time output monitoring

### Option 2: **Cairo CLI in Terminal**
- Run `cairo` directly in your terminal
- Same Cairo experience, full TUI features
- Works independently of VS Code

### Option 3: **VS Code Chat Participant**
- Type `@Cairo` in the Chat view
- Quick access without opening the sidebar
- Integrated with VS Code's chat interface

## 🚀 Installation

```bash
# From the Cairo repo, build and install the extension
bash scripts/install-extension.sh

# Or install a packaged VSIX manually
code --install-extension vscode-extension/.vscode-extension/cairo-vscode-*.vsix
```

## 📖 Usage

### VS Code Chat Panel

1. Click the **Cairo** icon in the sidebar (or press `Ctrl+Shift+P` → "Cairo: Open Chat")
2. Type your message in the input field
3. Use slash commands like `/new`, `/config`, `/reload`, and `/help`
4. Watch real-time responses stream in

### VS Code Chat View

1. Press `Ctrl+` (backtick) to open the Chat view
2. Type `@Cairo` to start a conversation
3. Use commands like `/new` for fresh sessions

### Terminal CLI

```bash
# Direct Cairo access
cairo

# Or with specific model
cairo --model qwen3.5:35b

# Check available models
ollama list
```

## ⚙️ Configuration

### Settings (`cairo-vscode` in VS Code Settings)

| Setting | Default | Description |
|---------|---------|-------------|
| `cairoExecutable` | Auto-detect | Path to Cairo binary |
| `ollamaUrl` | `http://localhost:11434` | Ollama API URL |
| `model` | `qwen3.5:35b` | Default model to use |

### Auto-Detection

The extension automatically finds Cairo in:
- `/usr/local/bin/cairo`
- `/usr/bin/cairo`
- Any directory in your `PATH`

## 🎨 Features

### VS Code Chat Panel
- **Persistent Process**: Cairo runs in background (no cold starts)
- **Streaming**: Real-time output as Cairo responds
- **Slash Commands**: Type `/` to choose commands from a filtered menu
- **Keyboard Flow**: Enter sends, Shift+Enter inserts a new line
- **Configuration**: Easy access to settings from the panel

### Terminal CLI
- **Full TUI**: Complete Cairo terminal interface
- **All Features**: Same capabilities as VS Code integration
- **Independence**: Works outside VS Code

### Chat Participant
- **Quick Access**: `@Cairo` command anywhere
- **Integrated**: Works with VS Code's chat UI
- **Commands**: `/new`, `/config`, etc.

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     VS Code Extension                        │
├─────────────────────────────────────────────────────────────┤
│  ┌──────────────────┐  ┌──────────────────┐  ┌───────────┐ │
│  │  Chat Panel    │  │  Chat Participant│  │  Settings │ │
│  │  (Sidebar)     │  │  (@Cairo)        │  │           │ │
│  └────────┬───────┘  └────────┬─────────┘  └───────────┘ │
│           │                   │                            │
│           └───────────┬───────┘                            │
│                       │                                    │
│              ┌────────▼────────┐                          │
│              │   Cairo         │                          │
│              │   Persistent    │                          │
│              │   Process       │                          │
│              └────────┬────────┘                          │
└───────────────────────┼───────────────────────────────────┘
                        │
                        ▼
              ┌──────────────────┐
              │   Ollama API     │
              │   (local/remote) │
              └──────────────────┘
```

## 🔧 Development

```bash
# Build and package the VSIX
bash scripts/build-extension.sh

# Watch for changes
npm run watch

# Install the latest packaged VSIX
bash scripts/install-extension.sh

# Build Cairo packages with the VSIX bundled
bash scripts/packaging/build-packages.sh
```

## 📝 Notes

- **No Re-spawning**: Cairo runs continuously for performance
- **Model Pinning**: Uses Ollama's keep-alive to avoid cold starts
- **Project-Local DB**: Each project can have its own Cairo identity
- **Offline-First**: All processing happens locally

## 🎯 When to Use Each Mode

### Use VS Code Chat Panel When:
- Working inside VS Code
- Want a dedicated Cairo sidebar
- Prefer visual interface
- Want real-time streaming

### Use Terminal CLI When:
- Working outside VS Code
- Need full TUI features
- Prefer keyboard shortcuts
- Want complete Cairo experience

### Use Chat Participant When:
- Need quick access
- Working in Chat view
- Want integrated commands
- Quick reference lookups

## 🤝 Contributing

This extension is maintained by daddy for personal and team use. Features and improvements welcome!

## 📄 License

MIT
