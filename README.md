# Encre

[![Build and Release](https://github.com/paradoxe35/encre/actions/workflows/build.yml/badge.svg)](https://github.com/paradoxe35/encre/actions/workflows/build.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A simple tool that fixes, translates and types your text with AI, anywhere on your computer.

## Why I Built This

English isn't my first language. Every time I write an email, a message, or some documentation, I end up with grammar mistakes or typos. I got tired of copying text to ChatGPT, waiting for a response, then copying it back.

I wanted something simpler: press a hotkey, and my text gets fixed. No switching apps, no copy-paste dance. Just write, press a key, done.

That's Encre. It sits in your system tray, listens for a hotkey, grabs your text, sends it to an AI, and replaces it with the corrected version. All in a few seconds.

It also translates, and it types what I say out loud. Same deal every time: one hotkey, no app switching.

## Demo

![Encre Demo](assets/demo.gif)

## What It Does

| Action              | What happens                                                          |
| ------------------- | --------------------------------------------------------------------- |
| Revise selection    | Select text, press the hotkey, it comes back corrected                |
| Revise everything   | Same thing, but it selects the whole field first                      |
| Translate selection | Translates between your two languages, choosing the direction for you |
| Dictate             | Hold the hotkey, talk, and your words are typed where the cursor is   |

Text is replaced in place, and your clipboard is put back the way you left it.

## How It Works

1. You're writing somewhere (email, browser, notes, anywhere)
2. Select your text (or use the "revise everything" hotkey)
3. Press the hotkey
4. Your text gets replaced with the AI-improved version

That's it.

## Installation

Download the [latest release](https://github.com/paradoxe35/encre/releases/latest) for your system:

- **Windows**: Run the installer or extract the portable ZIP
- **macOS**: Open the DMG (or unzip the ZIP), drag to Applications. First launch: right-click > Open. If macOS still refuses it, use System Settings > Privacy & Security > Open Anyway, or run `xattr -cr /Applications/Encre.app`
- **Linux**: Use the .deb, .rpm or Arch package, the AppImage, or the portable archive

On first launch, grant accessibility/input permissions when prompted - this is needed for global hotkeys to work.

### Linux Users

**Wayland users only**: You need to be in the `input` group for hotkeys to work:

```bash
sudo usermod -aG input $USER
# Then log out and log back in
```

X11 users don't need this - hotkeys work out of the box.

**Required packages** (Debian/Ubuntu - the .deb installs these automatically):

```bash
sudo apt install libgl1 libx11-6 libxext6 libxcb1 libxinerama1 libxtst6 libxdo3 libxkbcommon0 libxi6 libxcursor1 libxrandr2 libxrender1 libxfixes3 libxxf86vm1 libasound2
```

## Quick Start

1. Launch Encre (it appears in your system tray)
2. Right-click the tray icon > Settings
3. Under **AI**, add your API key for OpenAI, Claude, Gemini, or OpenRouter
4. For dictation, switch it on under **Hotkeys**, then download a model under **Speech**
5. Start writing somewhere, select text, press the hotkey

## Default Hotkeys

| Action              | Linux              | Windows            | macOS               |
| ------------------- | ------------------ | ------------------ | ------------------- |
| Revise selection    | `Ctrl+Super`       | `Ctrl+Win`         | `Ctrl+Cmd`          |
| Revise everything   | `Ctrl+Alt+Space`   | `Ctrl+Alt+Space`   | `Ctrl+Option+Space` |
| Translate selection | `Ctrl+Alt+G`       | `Ctrl+Alt+G`       | `Ctrl+Option+G`     |
| Dictate             | `Ctrl+Shift+Space` | `Ctrl+Shift+Space` | `Ctrl+Shift+Space`  |

Change them in Settings > Hotkeys. Translate and dictate ship switched off - turn them on there when you want them.

## Supported AI Providers

| Provider   | Example Models                                   |
| ---------- | ------------------------------------------------ |
| OpenAI     | gpt-4o, gpt-4o-mini                              |
| Claude     | claude-3-5-haiku, claude-3-5-sonnet              |
| Gemini     | gemini-2.5-flash, gemini-2.5-flash-lite          |
| OpenRouter | google/gemini-2.5-flash, any model it serves     |

You can also add custom OpenAI-compatible providers (local LLMs, Together AI) with their own base URL, and mark them as needing no API key.

A few things worth knowing:

- Each action can use a different provider, or just follow the default
- Start a selection with `@name` to send that one request to a specific provider

## Prompts

Each action has its own prompt, editable under Settings > Actions. Leave one empty to use the built-in.

Translation works from a language pair you pick (88 languages). It detects which of the two you wrote in and translates to the other, so one hotkey covers both directions.

## Dictation

Hold the dictate hotkey, talk, release. The transcript is typed at your cursor. You can switch it to toggle mode if you prefer press-to-start, press-to-stop.

Transcription runs **locally by default** - audio never leaves your machine. There are 69 models to choose from (Whisper, Parakeet, Moonshine, Voxtral and others), listed fastest-on-your-machine first.

- Some models transcribe while you speak, so the text is ready as soon as you release the key
- Drop your own `.gguf` or `.bin` into `~/.encre/models` and it shows up in the list
- Prefer a hosted service? Point Speech at OpenAI, Groq, or anything OpenAI-compatible
- Optionally clean the transcript up with your AI provider before it's typed

## History

Settings > History keeps the last 200 actions - what you sent, what came back, which model handled it. It's a local file; nothing is uploaded.

## Configuration

Everything lives in one folder, created on first run:

| Platform      | Path                    |
| ------------- | ----------------------- |
| Linux / macOS | `~/.encre/`             |
| Windows       | `%USERPROFILE%\.encre\` |

It holds your settings and API keys (`config.json`), downloaded speech models (`models/`), recent actions and logs.

## Building From Source

You'll need:

- Go 1.24+ with `CGO_ENABLED=1`
- Rust toolchain (stable)
- CMake
- Platform dependencies (see `rust-ffi/README.md`)

```bash
make build    # Build for current platform
make run      # Run locally
make test     # Run tests
```

The app is a Go frontend (Fyne UI) over a Rust core that handles hotkeys, clipboard, key simulation and speech.

## Troubleshooting

**Hotkeys not working?**

- Check that only one instance is running (look in system tray)
- On macOS: grant Accessibility permissions in System Settings
- On Linux Wayland: make sure you're in the `input` group (X11 doesn't need this)

**Revisions failing?**

- Check your API key is valid
- Look at logs in `~/.encre/logs/`

**Dictation not working?**

- Make sure a model is downloaded in Settings > Speech
- Check the right microphone is selected there
- On Linux, install `libasound2` if it's missing

## License

MIT - see [LICENSE](LICENSE)
