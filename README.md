# Current

> **In development.** Current is an early, actively changing RSS reader. Expect rough edges and back up your library before updating. macOS support is being tested; this is not a stable release.

[![Build and test](https://github.com/opossumactual/current/actions/workflows/check.yml/badge.svg)](https://github.com/opossumactual/current/actions/workflows/check.yml)

**One library. Two ways to read.** A personal RSS reader with a graphical reading room and a terminal interface with real Kitty image previews. Both clients share the same local SQLite library, subscriptions, read state, and saved articles.

- **Graphical:** Svelte and TypeScript, light/dark themes, responsive three-pane reading, search, saved articles, focus mode, adjustable text size, and full-text extraction.
- **Terminal:** Rust and Ratatui, live Omarchy theme colors, keyboard navigation, asynchronous loading, Kitty images with a text-image fallback, search, read/save/undo, and a dedicated feed manager.
- **Shared server:** Go, SQLite FTS, RSS/Atom/JSON feeds, website feed discovery, scheduled updates, per-host throttling, conditional HTTP requests, and retry backoff.

## macOS quick start (Apple Silicon)

Homebrew is used for dependencies; Current does not need a Homebrew formula or tap. If Apple's Command Line Tools are not installed, run `xcode-select --install` and finish the installer first.

```sh
brew install go rust node pnpm python
brew install --cask kitty

git clone https://github.com/opossumactual/current.git
cd current
./scripts/setup.sh
./current terminal
```

Already in Kitty? Use `./current tui` to open Current in that window. For the graphical reader, use `./current web` to open your default browser. Kitty is optional for the browser; the terminal falls back to text images when graphics are unsupported.

**Every fresh installation starts with an empty feed list.** No personal subscriptions, articles, reading history, credentials, or library database are included. Add your own feeds with **G → A** in the terminal, or **Add feed** in the browser. Both interfaces also support importing an OPML file.

On macOS the library is stored in `~/Library/Application Support/Current`, outside the checkout. `./current paths` shows the exact library and log locations. The classic theme works out of the box; `CURRENT_THEME_FILE` can select a custom palette.

To update:

```sh
./current backup
git pull --ff-only
./scripts/setup.sh
./current restart
```

Reopen the terminal reader or reload the browser after updating. This setup builds locally on your Mac; it does not install a login service or synchronize with another computer.

## Linux and general setup

Build requirements: Go 1.27.1+, Rust 1.90+, Node 22.12+ (Node 24 recommended), pnpm 10, Python 3.9+, and a C compiler/linker. The launcher uses Bash and Python's standard library; no pip packages, external `flock`, or `setsid` utilities are needed. Curl is used as a fallback for some feed publishers. Kitty is needed for `terminal`; `tui` runs in an existing terminal.

```sh
./current build
./current web        # open the graphical reader
./current terminal   # open Current in Kitty
./current tui        # use this terminal instead
```

The readers start the shared server automatically at **http://127.0.0.1:8490**. A fresh checkout starts with an empty library. Add a feed or import an OPML subscription file to begin.

```sh
./current start     # start the shared server without opening a reader
./current status
./current restart   # use a newly built server
./current stop
./current serve     # run in the foreground; Ctrl+C stops it
./current backup    # consistent SQLite backup while Current is running
```

Closing the browser or terminal reader leaves the server running and polling. `stop` stops polling. There is no automatic startup service installed. After rebuilding, restart the server, refresh the browser, and reopen terminal clients.

The terminal automatically follows your active Omarchy theme, including the reader, feed manager, and link picker. It checks for palette changes once a second, supports light and dark themes, and adjusts muted text where needed for readability. It reads `~/.local/state/omarchy/current/theme/colors.toml` (with support for XDG paths and the older `~/.config/omarchy/current/theme/colors.toml` location). No Omarchy hooks or desktop configuration changes are needed. The browser retains its own light/dark appearance controls.

Without an Omarchy palette, Current uses its original colors. Use `./current terminal --classic-theme` to explicitly keep those colors, or set `CURRENT_THEME_FILE=/path/to/colors.toml` to use another Omarchy-format palette. `bin/current-tui --theme-check` prints the detected source and resolved colors without opening the reader.

## Manage your sources

In the graphical reader, **Add feed** is at the top of the sidebar and **Manage feeds** is at the bottom. On a narrow screen, open the navigation menu first. **A** and **G** open these screens from the keyboard.

In the terminal, press **G** for the feed manager:

| Key | Action |
| --- | --- |
| J / K or arrows | Select a feed or collection |
| A | Discover and subscribe to a website or feed URL |
| E / Enter | Edit name, URL, collection, polling interval, and full-text preference |
| P | Pause/resume, or restore an unsubscribed feed |
| R | Update the selected feed now |
| D | Unsubscribe, with confirmation |
| Tab / C | Switch between feeds and collections |
| N | Create a collection |
| / | Find a feed |
| V | Show unsubscribed feeds |
| I / O | Import/export an OPML file (`~/` paths work) |
| Esc | Close a form or return to reading |

Forms use **Tab** to move between fields, **Left/Right** or **Space** to change choices, **Ctrl+U** to clear a field, and **Enter/Ctrl+S** to submit. Terminal exports require a new filename so an existing export is not overwritten.

Both managers support creating, renaming, and removing collections. Removing a collection moves its feeds to Uncategorized. **Unsubscribing retains all articles and saved items**; use the unsubscribed view to restore the subscription. Old articles remain searchable and appear in All/Saved views. Importing OPML also restores previously unsubscribed feeds and skips existing subscriptions.

## Read with the keyboard

| Key | Action |
| --- | --- |
| J / K | Next/previous article; in terminal source pane, switch source immediately |
| S / M / U | Save, toggle read, undo |
| / | Search (Enter submits in terminal) |
| Z | Focus reading mode |
| O | Open original article |
| R | Terminal: refresh feeds; browser: reload articles and shared state |
| ? | Keyboard help |

Additional terminal keys: **Tab** switches panes, **F** switches collections/feeds, **1/2/3** select All/Unread/Saved, **PgUp/PgDn** scroll the article, **N** loads more results, **I** toggles image previews, and **T** loads full article text. **Q** quits. `./current tui --text-images` forces a half-block image fallback.

**Ctrl+P** opens the searchable command menu. Type words such as `refresh`, `image`, `add feed`, or `mark all`; use **Up/Down** to select and **Enter** to run. Shortcuts and explanations appear alongside commands. Unavailable actions explain why, and bulk actions still show their confirmation.

**R** (or **Shift+R**) fetches active feeds immediately and brings the latest stories into the current list when complete. The header shows completed feeds, new stories, and failures. Repeated requests join the running refresh. The selected article and its reading position stay in place. Kitty also displays refresh progress along its window edge. **Ctrl+R** reloads cached stories without fetching publishers.

**[ / ]** opens the previous/next article while keeping you in the reader. These keys follow the loaded reading queue for the selected source, search, and filter; returning restores your position even after a story leaves Unread. New arrivals do not rearrange this queue. **N** extends it with the next page. Selecting another headline or changing the source/filter starts a new queue.

Reader headlines use Kitty's native double-size text when the terminal reports support and the window has enough vertical room. **Shift+H** toggles the size for this session; launch with `./current tui --small-headlines` to start with normal titles. Short windows and unsupported terminals use normal text automatically.

**V** opens the article image viewer. **[ / ]** selects an image within the gallery, **+ / -** zooms, **arrows or H/J/K/L** pan, and **0** fits the image to the window without enlarging small images. **1** shows actual pixels (100%); the header shows the source dimensions and zoom relative to the original image. **O** opens the image in your browser, **R** retries a failed image, and **Esc** returns to the article. Captions come from the article where available. The gallery includes images from loaded full text and feed content, with the lead image first. It prefers larger advertised image variants (`srcset`, compatible `<picture>` sources, and direct links to originals), and uses sharper Lanczos scaling. Existing stored thumbnails are upgraded when their article HTML contains the larger variant. It displays still images, including the first frame of GIFs; animation playback is not included.

The terminal adapts to the window width: **170+ columns** shows sources, headlines, and the reader; **110–169 columns** shows headlines and the reader; **65–109 columns** shows one pane at a time. Press **C** to open sources and **F** to switch between collections and individual feeds. **Enter** moves from sources to headlines, or opens the selected article; **Esc** returns to the previous pane. Returning from the reader preserves your search, selection, and reading position. **Tab/Shift+Tab** cycles through the available panes. **?** opens scrollable keyboard help. The minimum window size is 65 columns by 18 rows.

To open a numbered article link in the terminal, press **B**, type its number, and press **Enter**—for example, **b → 12 → Enter** opens reference `[12]` in your default browser. You can also select with **J/K** or the arrow keys; **Esc** returns to reading. The picker shows destination URLs and follows the article's footnote numbering, including after loading full text with **T**. **O** opens the original article.

The terminal shows **● UNREAD** with a bold headline and **✓ READ** with a muted headline. Saved articles have a separate **★ SAVED** label. A successfully loaded article is automatically marked read after its reader has been visible for one second; rapidly passing over headlines or browsing the source pane does not mark them. In the narrow single-pane layout, **Enter** opens the reader and starts that delay. **M** toggles read/unread and **U** undoes the last reading-state action. Marking the current story unread keeps it unread until you leave and reopen it.

Press **2** for an unread queue. A story you finish stays visible while you read it, then leaves the queue when you move to another story. Counts update after the change is saved. **1** shows both read and unread stories again.

In the terminal, **Shift+M** marks all articles read in the selected feed or collection; select **All sources** to clear the entire library. A confirmation names the scope: **Enter/Y** confirms and **Esc/N** cancels. It covers every page and includes articles outside the current search or Saved filter. Articles and saved items remain in the library. This bulk action clears the session's undo history and cannot be undone with **U**.

The web refresh button starts feed updates. New arrivals produce a “Show latest” notice so they don't move the article you are reading. The selected article's read/save state and source metadata synchronize between clients every four seconds; **Ctrl+R** in the terminal reloads a filtered article list after changes elsewhere. Undo history and reading positions are session-local.

## Data and polling

On Linux, the launcher keeps the library in `data/library.db`, image previews in `data/images/`, and server logs in `data/server.log` within the checkout. On macOS these files are under `~/Library/Application Support/Current`. Use `./current paths` to locate them. These files, OPML exports, dependencies, screenshots, and compiled binaries are ignored by Git. No external database or Node server is required at runtime: the Go binary embeds the built graphical client.

```sh
CURRENT_DATA_DIR="$HOME/.local/share/current" ./current web
CURRENT_ADDR=127.0.0.1:8492 ./current serve
```

Use the same data directory and address for all launcher commands. The standalone terminal binary honors `CURRENT_URL`. The standalone server accepts `--addr`, `--data`, and `--poll-interval`; see `bin/current-server --help`.

The scheduler checks for due feeds every 15 seconds and uses four workers. Subscriptions default to hourly updates; each can be set to 5–1440 minutes. Paused and unsubscribed feeds are excluded from scheduled and global updates. An explicit “Update now” can fetch a paused feed. Errors back off and host throttling respects Retry-After. Feed details show the last attempt, last success, next check, and error. Live updates require the server to remain running.

Use `./current backup` before major upgrades. It uses SQLite's backup API, including committed WAL content. To restore, stop Current, move the current database **and its `-wal`/`-shm` sidecars** to a backup location, and put the selected backup at the library path shown by `./current paths`. Cached images can be regenerated. OPML exports contain subscriptions and collections, not article history or reading state.

## Development and verification

```sh
make build          # web assets, embedded Go server, terminal binary
make check          # Svelte diagnostics, Go vet/race tests, Rust format/tests
python3 scripts/test_launcher.py  # isolated lifecycle and empty-library checks
pnpm --dir web exec playwright install chromium
make integration    # browser + terminal workflows against a temporary library
```

GitHub Actions builds and tests fresh checkouts on Linux and Apple Silicon macOS. The launcher tests cover paths with spaces, concurrent startup, safe shutdown, backups, and an empty initial library. Interactive Kitty rendering on physical Macs still benefits from manual testing.

The integration harness starts a deterministic local feed server and a separate Current instance, tests real subscription/collection management, failures, archive/restore, reading state, OPML, mobile layout, and terminal keyboard forms, then removes its temporary library. It writes preview captures to ignored `screenshots/`.

For frontend development, run the backend and `pnpm --dir web dev`; Vite proxies `/api` to port 8490. Source lives in `cmd/current/`, `internal/`, `web/src/`, and `tui/src/`. `make format` formats all three languages.

## Scope

Current currently serves a single personal library and only accepts a loopback listening address. Remote hosting, multiple users, synchronization with FreshRSS, and background OS startup are not implemented. The terminal displays a lead image rather than laying out full HTML. Publisher images and full-text retrieval can fail; the original link remains available. Search indexes titles and feed content; separately extracted full text is currently not indexed.

Current's backend began from the Rill project. Its web and terminal interfaces share one local library. Personal data stays outside version control.
