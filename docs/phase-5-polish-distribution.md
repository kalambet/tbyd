# Phase 5 — Polish & Distribution

> **Goal:** The system is ready for use beyond the developer themselves. Onboarding is smooth, data is encrypted at rest, the project has a proper identity, and installation is a single command.

---

## Issue 5.1 — Project rename and identity

**Context:** The repo is currently `tbyd` (placeholder). Before public distribution, the project needs a final name, logo, and consistent identity.

**Tasks:**
- Finalize project name (candidates in PLAN.md — recommend `eidolon` or `anima`)
- Update:
  - `go.mod` module path
  - All `package` imports across the codebase
  - Binary name in `Makefile`
  - Xcode project name and bundle ID (`com.yourname.<projectname>`)
  - UserDefaults domain (`com.tbyd.app` → `com.<name>.app`)
  - Data directory name (`Application Support/TBYD/` → `Application Support/<Name>/`)
- Create minimal brand assets:
  - Menubar icon (SVG, exported at 16x16 and 32x32 for retina) — must work in both light and dark mode
  - App icon (1024x1024 for App Store, all required sizes)
- Update README.md with project name, one-sentence description, and installation instructions

**Acceptance criteria:**
- `go build ./...` succeeds after rename
- No references to the old name remain in code or config
- Menubar icon renders clearly in both light and dark macOS themes

---

## Issue 5.2 — Onboarding flow

**Context:** A first-time user should be able to go from zero to their first enriched query in under 5 minutes. The onboarding flow handles prerequisite checks, API key setup, and first data entry.

**Tasks:**
- On first launch (no UserDefaults values found): show onboarding wizard in SwiftUI
- Onboarding steps:
  1. **Welcome** — one-screen explanation of what the app does and what data stays local
  2. **Prerequisites check** — verify Ollama is installed; if not, show install link and "Check again" button; if yes, green checkmark. Note: tbyd requires Ollama to be running — it does not manage the Ollama process
  3. **API key setup** — OpenRouter API key field with link to get one; stores in Keychain on save; verifies by calling `GET /v1/models`
  4. **API token generation** — generate localhost bearer token, store in Keychain, show token for browser extension setup
  5. **Model download** — shows download progress for `phi3.5` and `nomic-embed-text` (Ollama pull); skippable with "use later" option
  6. **Profile quick-start** — 3 optional fields: role/title, primary domain, communication preference (dropdown); "Skip" available
  7. **Data collection consent** — clear explanation of what's stored and where; toggle for `save_interactions`; "I understand, continue" button
  8. **Done** — shows MCP setup snippet for Claude Code; "Open Data Browser"; "Start using tbyd"
- Mark onboarding complete: `defaults write com.tbyd.app app.onboarding_complete -bool true`
- CLI equivalent: `tbyd setup` walks through the same steps in the terminal

**Swift unit tests** (`macos/Tests/OnboardingTests/`):
- `OnboardingViewModelTests.testInitialStep` — verify first step is `.welcome` on fresh install
- `OnboardingViewModelTests.testOllamaCheckPasses` — mock health check returns ok; verify step advances to API key
- `OnboardingViewModelTests.testOllamaCheckFails` — mock health check fails; verify step stays on prerequisites, error shown
- `OnboardingViewModelTests.testAPIKeyValidation_Valid` — mock `/v1/models` returns 200; verify step advances
- `OnboardingViewModelTests.testAPIKeyValidation_Invalid` — mock returns 401; verify error message shown, step does not advance
- `OnboardingViewModelTests.testSkipProfile` — tap skip on profile step; verify step advances without profile data set
- `OnboardingViewModelTests.testCompletionWritesConfig` — complete all steps; verify `app.onboarding_complete = true` written to UserDefaults

**Acceptance criteria:**
- First-time user can complete onboarding in < 5 minutes
- Skipping profile fields still results in a working system (profile is optional)
- API key validation provides a clear error if key is invalid
- After onboarding, `GET /health` returns `{"status":"ok"}` and the first query works

---

## Issue 5.3 — Encryption at rest

**Context:** The local knowledge base and interaction log contain sensitive personal information. It must be encrypted at rest so that if the disk is compromised, data is not readable without authentication.

**Tasks:**
- Evaluate options:
  - **SQLite encryption**: `SQLCipher` (CGO — undesirable); `go-sqlcipher` wrapper; or **file-level encryption**
  - **Recommended approach**: encrypt the SQLite database file at the OS level using macOS Data Protection
    - Create `~/Library/Application Support/<name>/` with protection class `NSFileProtectionComplete`
    - This ensures data is unreadable when device is locked (macOS FileVault must be enabled)
    - Zero implementation overhead; no CGO
- For users without FileVault:
  - Warn during onboarding: "Enable FileVault for full data protection"
  - Store API keys in macOS Keychain (already done in Issue 0.2)
  - The localhost API bearer token is stored in macOS Keychain alongside the OpenRouter API key
  - Do NOT store API keys in UserDefaults
- For sensitive content flagged by user (future extension):
  - Allow per-document encryption using `golang.org/x/crypto/nacl/secretbox`
  - Key derived from macOS Keychain entry
- Implement `internal/storage/secure.go`:
  - `SecureDataDir(dir string) error` — sets macOS data protection attributes via `xattr`
  - Called on database initialization

**Unit tests** (`internal/storage/secure_test.go`):
- `TestSecureDataDir_SetsAttribute` — mock `xattr` syscall; verify called with correct attribute name and value
- `TestSecureDataDir_DirectoryMissing` — pass non-existent directory; verify error returned (not panic)
- `TestNoAPIKeyInLogs` — run server with a real config; capture all log output; verify no string matching API key pattern appears at any log level

**Acceptance criteria:**
- Database directory has `NSFileProtectionComplete` attribute set (verify with `xattr -l`)
- API keys are stored in Keychain, not in UserDefaults
- Warning displayed during onboarding if FileVault is not enabled
- No API keys appear in log output at any log level

---

## Issue 5.4 — Homebrew formula

**Context:** The primary installation method for macOS power users is Homebrew. The formula should handle the Go binary installation; Ollama is listed as a prerequisite.

**Tasks:**
- Create `Formula/eidolon.rb` (or project name) in repo (or in a separate `homebrew-eidolon` tap repo):
  ```ruby
  class Eidolon < Formula
    desc "Local-first data sovereignty layer for cloud LLM interactions"
    homepage "https://github.com/kalambet/tbyd"
    url "https://github.com/kalambet/tbyd/archive/refs/tags/v0.1.0.tar.gz"
    sha256 "..."
    license "MIT"

    depends_on "go" => :build
    depends_on "ollama"

    def install
      system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/tbyd"
    end

    service do
      run [opt_bin/"tbyd", "start"]
      keep_alive true
      log_path var/"log/tbyd.log"
      error_log_path var/"log/tbyd.log"
    end

    def caveats
      <<~EOS
        To start tbyd as a background service:
          brew services start eidolon

        On first run, complete setup with:
          tbyd setup

        Add to Claude Code:
          claude mcp add tbyd --url http://localhost:4001
      EOS
    end
  end
  ```
- Set up Homebrew tap: `brew tap kalambet/eidolon` → `brew install eidolon`
- Set up GitHub Actions workflow:
  - On tag push: build macOS binaries (ARM64 + AMD64), create GitHub Release, compute SHA256, update formula
- Verify formula installs cleanly on a fresh macOS environment

**Acceptance criteria:**
- `brew tap kalambet/eidolon && brew install eidolon` installs the binary
- `brew services start eidolon` starts tbyd as a background service
- `tbyd setup` runs successfully post-install
- Formula passes `brew audit --strict`

---

## Issue 5.5 — macOS App Store preparation (optional)

**Context:** App Store distribution enables auto-updates, wider reach, and Apple notarization. This is optional but desirable for the SwiftUI menubar app.

**Tasks:**
- Enable App Sandbox entitlement in Xcode project
  - Add required entitlements: `com.apple.security.network.client` (for localhost calls), `com.apple.security.files.user-selected.read-write`
  - App Group for Share Extension + main app communication
- App Review considerations:
  - Declare that app communicates with localhost only (no external network for user data)
  - Privacy manifest (`PrivacyInfo.xcprivacy`): declare collected data types, all marked "not linked to user"
- Notarize standalone binary (Homebrew distribution) using `xcrun notarytool`
- Create App Store screenshots for all required sizes
- Write App Store description emphasizing local-first, privacy, data sovereignty

**Swift unit tests** (`macos/Tests/SandboxTests/`):
- `SandboxEntitlementTests.testNetworkClientEntitlement` — parse entitlements plist; verify `com.apple.security.network.client` is `true`
- `SandboxEntitlementTests.testNoExcessiveEntitlements` — verify only declared entitlements present; fail if unexpected ones added (prevents accidental privilege escalation)
- `PrivacyManifestTests.testAllDataTypesNotLinkedToUser` — parse `PrivacyInfo.xcprivacy`; verify every collected data type has `NSPrivacyCollectedDataTypeLinked = false`

**Acceptance criteria:**
- App runs under sandbox without functionality loss
- Share Extension works under sandbox
- Notarization succeeds for standalone binary
- App Store submission checklist completed

---

## Issue 5.6 — Comprehensive documentation

**Context:** The system has enough moving parts that good documentation is essential for users and contributors.

**Tasks:**
- `README.md`:
  - One-paragraph intro: what problem it solves, who it's for
  - Prerequisites: macOS 14+, Ollama, OpenRouter API key
  - Quick start: brew install, tbyd setup, MCP configuration
  - Architecture diagram (simplified version from PLAN.md)
  - FAQ: Is my data sent anywhere? What if Ollama isn't running? How do I delete all data?
- `docs/architecture.md` — comprehensive technical reference (promote PLAN.md content)
- `docs/privacy.md` — detailed data flow documentation:
  - What is stored, where, in what format
  - What reaches OpenRouter (enriched prompts only)
  - How to export/delete all data
- `docs/mcp-setup.md` — step-by-step Claude Code MCP integration
- `docs/fine-tuning.md` — how to prepare data and run fine-tuning
- `docs/vectorstore-migration.md` — Vector store migration guide (SQLite → LanceDB)
- `docs/security.md` — Localhost auth model, Keychain usage, CSRF prevention
- `CONTRIBUTING.md` — dev setup, testing, PR process
- In-app help: "?" button in each SwiftUI view links to relevant docs section

**Documentation tests** (`docs/tests/`):
- `test_readme_links.sh` — verify all markdown links in README.md resolve (no 404s)
- `test_cli_help_coverage.sh` — run `tbyd --help` and each subcommand `--help`; verify all commands documented in README are present in help output
- `test_default_config_valid.sh` — load config with empty backend; verify all defaults are valid and no errors

**Acceptance criteria:**
- A non-technical user can follow README to get their first enriched query in < 10 minutes
- Privacy doc clearly explains every data flow in plain language
- All CLI commands are documented in `--help` output

---

## Phase 5 Verification

1. Fresh macOS machine: `brew tap ... && brew install ...` → `tbyd setup` → send first query → verify enrichment works
2. Verify no plaintext API keys in UserDefaults or logs
3. Verify `xattr -l ~/Library/Application\ Support/<name>/` shows NSFileProtectionComplete
4. Uninstall via `brew uninstall` → verify all processes stopped
5. Re-install → verify data from previous installation is preserved in Application Support
6. README quick-start: follow it on a fresh machine, noting any gaps
7. `go test ./...` passes
8. `go test -tags integration ./...` passes
9. Swift tests pass: `xcodebuild test -scheme tbyd -destination 'platform=macOS'`
10. Documentation tests pass: `bash docs/tests/test_readme_links.sh`
11. Verify bearer token is stored in Keychain, not in UserDefaults
