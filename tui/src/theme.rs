use ratatui::style::Color;
use std::{
    collections::HashMap,
    env, fs,
    path::{Path, PathBuf},
    time::{Duration, Instant},
};

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Palette {
    pub bg: Color,
    pub panel: Color,
    pub line: Color,
    pub text: Color,
    pub muted: Color,
    pub accent: Color,
    pub selection: Color,
}
impl Default for Palette {
    fn default() -> Self {
        Self {
            bg: Color::Rgb(22, 28, 32),
            panel: Color::Rgb(27, 35, 40),
            line: Color::Rgb(53, 66, 73),
            text: Color::Rgb(217, 226, 222),
            muted: Color::Rgb(137, 155, 157),
            accent: Color::Rgb(224, 171, 113),
            selection: Color::Rgb(53, 66, 73),
        }
    }
}

type Rgb = [u8; 3];
fn hex(value: &str) -> Option<Rgb> {
    let value = value.strip_prefix('#')?;
    if value.len() != 6 || !value.is_ascii() {
        return None;
    }
    Some([
        u8::from_str_radix(&value[0..2], 16).ok()?,
        u8::from_str_radix(&value[2..4], 16).ok()?,
        u8::from_str_radix(&value[4..6], 16).ok()?,
    ])
}
fn color(rgb: Rgb) -> Color {
    Color::Rgb(rgb[0], rgb[1], rgb[2])
}
fn mix(a: Rgb, b: Rgb, percent: u16) -> Rgb {
    std::array::from_fn(|i| {
        ((a[i] as u16 * (100 - percent) + b[i] as u16 * percent + 50) / 100) as u8
    })
}
fn luminance(rgb: Rgb) -> f64 {
    let linear = rgb.map(|v| {
        let v = v as f64 / 255.0;
        if v <= 0.04045 {
            v / 12.92
        } else {
            ((v + 0.055) / 1.055).powf(2.4)
        }
    });
    linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722
}
fn contrast(a: Rgb, b: Rgb) -> f64 {
    let (a, b) = (luminance(a), luminance(b));
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}
fn readable(candidate: Rgb, text: Rgb, surfaces: &[Rgb]) -> Rgb {
    for percent in (0..=100).step_by(5) {
        let rgb = mix(candidate, text, percent);
        if surfaces.iter().all(|&bg| contrast(rgb, bg) >= 4.5) {
            return rgb;
        }
    }
    text
}

impl Palette {
    pub fn parse(input: &str) -> Option<Self> {
        // Omarchy's palette is a flat key/value file. Read only quoted hex
        // colors (with optional inline comments), never theme commands/configs.
        let mut values = HashMap::new();
        for line in input.lines() {
            let line = line.trim();
            if line.starts_with('#') {
                continue;
            }
            let Some((key, value)) = line.split_once('=') else {
                continue;
            };
            let value = value.trim();
            let Some(quote @ ('\'' | '"')) = value.chars().next() else {
                continue;
            };
            let Some(end) = value[1..].find(quote) else {
                continue;
            };
            if let Some(rgb) = hex(&value[1..end + 1]) {
                values.insert(key.trim().trim_matches(['\'', '"']), rgb);
            }
        }
        let get = |keys: &[&str]| keys.iter().find_map(|key| values.get(key).copied());
        // Require both base colors before applying a newly written palette.
        let bg = get(&["background", "bg", "color0"])?;
        let mut text = get(&["foreground", "fg", "color7"])?;
        if contrast(text, bg) < 4.5 {
            text = if contrast([0; 3], bg) > contrast([255; 3], bg) {
                [0; 3]
            } else {
                [255; 3]
            };
        }
        let mut panel =
            get(&["lighter_background", "lighter_bg"]).unwrap_or_else(|| mix(bg, text, 4));
        if contrast(text, panel) < 4.5 {
            panel = bg;
        }
        let mut selection = get(&["selection", "selection_background", "color8"])
            .unwrap_or_else(|| mix(panel, text, 12));
        if contrast(text, selection) < 4.5 {
            selection = (0..=100)
                .step_by(5)
                .map(|amount| mix(selection, panel, amount))
                .find(|&surface| contrast(text, surface) >= 4.5)
                .unwrap_or(panel);
        }
        let surfaces = [bg, panel, selection];
        let muted = readable(
            get(&["muted", "dark_foreground", "dark_fg", "color8"])
                .unwrap_or_else(|| mix(panel, text, 65)),
            text,
            &surfaces,
        );
        let accent = readable(
            get(&["accent", "blue", "color4"]).unwrap_or(text),
            text,
            &surfaces,
        );
        let line = mix(panel, text, 20);
        Some(Self {
            bg: color(bg),
            panel: color(panel),
            line: color(line),
            text: color(text),
            muted: color(muted),
            accent: color(accent),
            selection: color(selection),
        })
    }
}

pub struct Theme {
    pub colors: Palette,
    paths: Vec<PathBuf>,
    source: Option<PathBuf>,
    last_input: String,
    last_poll: Instant,
}
impl Theme {
    pub fn from_env() -> Self {
        if env::args().any(|s| s == "--classic-theme") {
            return Self::new(vec![]);
        }
        if let Some(path) = env::var_os("CURRENT_THEME_FILE").filter(|s| !s.is_empty()) {
            return Self::new(vec![PathBuf::from(path)]);
        }
        let home = env::var_os("HOME").map(PathBuf::from);
        let state = env::var_os("XDG_STATE_HOME")
            .map(PathBuf::from)
            .filter(|p| p.is_absolute())
            .or_else(|| home.as_ref().map(|p| p.join(".local/state")));
        let config = env::var_os("XDG_CONFIG_HOME")
            .map(PathBuf::from)
            .filter(|p| p.is_absolute())
            .or_else(|| home.as_ref().map(|p| p.join(".config")));
        let mut paths = vec![];
        if let Some(state) = state {
            paths.push(state.join("omarchy/current/theme/colors.toml"));
        }
        // Current Omarchy uses a fixed HOME path; older installs used .config.
        if let Some(home) = &home {
            let path = home.join(".local/state/omarchy/current/theme/colors.toml");
            if !paths.contains(&path) {
                paths.push(path);
            }
        }
        if let Some(config) = config {
            paths.push(config.join("omarchy/current/theme/colors.toml"));
        }
        Self::new(paths)
    }
    pub fn new(paths: Vec<PathBuf>) -> Self {
        let mut theme = Self {
            colors: Palette::default(),
            paths,
            source: None,
            last_input: String::new(),
            last_poll: Instant::now(),
        };
        theme.reload();
        theme
    }
    pub fn source(&self) -> Option<&Path> {
        self.source.as_deref()
    }
    pub fn poll(&mut self) {
        if self.last_poll.elapsed() >= Duration::from_secs(1) {
            self.last_poll = Instant::now();
            self.reload();
        }
    }
    fn reload(&mut self) {
        for path in &self.paths {
            let input = match fs::read_to_string(path) {
                Ok(input) => input,
                // Omarchy briefly removes current/theme while replacing it.
                // Retain the last good colors instead of flashing the fallback.
                Err(_) if self.source.as_ref() == Some(path) => return,
                Err(_) => continue,
            };
            if self.source.as_ref() == Some(path) && input == self.last_input {
                return;
            }
            let Some(colors) = Palette::parse(&input) else {
                return;
            };
            self.colors = colors;
            self.source = Some(path.clone());
            self.last_input = input;
            return;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::{SystemTime, UNIX_EPOCH};

    const DARK: &str = "background = '#241f28'\nforeground = '#f2d6c4'\nlighter_background = '#33293a'\naccent = '#e78c64'\nmuted = '#7a6a7c'\nselection = '#4a3a4e'\n";
    const LIGHT: &str = "background = '#ffffff'\nforeground = '#000000'\nlighter_background = '#eeeeee'\naccent = '#444444'\nmuted = '#808080'\nselection = '#c0c0c0'\n";
    fn rgb(c: Color) -> Rgb {
        if let Color::Rgb(r, g, b) = c {
            [r, g, b]
        } else {
            panic!("expected RGB color")
        }
    }
    struct Fixture(PathBuf);
    impl Fixture {
        fn new() -> Self {
            let path = env::temp_dir().join(format!(
                "current-theme-{}-{}",
                std::process::id(),
                SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            ));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
        fn write(&self, name: &str, text: &str) -> PathBuf {
            let path = self.0.join(name);
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(&path, text).unwrap();
            path
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn semantic_and_legacy_palettes_resolve_the_same_colors() {
        let semantic = Palette::parse("background = \"#101010\" # comment\nforeground = '#eeeeee'\naccent = '#ffbb77'\nlighter_background = '#181818'\nmuted = '#999999'\nselection = '#222222'").unwrap();
        let aliases = Palette::parse("bg = '#101010'\nfg = '#eeeeee'\ncolor4 = '#ffbb77'\nlighter_bg = '#181818'\ndark_fg = '#999999'\nselection_background = '#222222'").unwrap();
        assert_eq!(semantic, aliases);
        assert_eq!(semantic.bg, Color::Rgb(16, 16, 16));
        assert_eq!(semantic.accent, Color::Rgb(255, 187, 119));
        let ansi = Palette::parse(
            "color0 = '#101010'\ncolor7 = '#eeeeee'\ncolor4 = '#ffbb77'\ncolor8 = '#222222'",
        )
        .unwrap();
        assert_eq!(ansi.bg, semantic.bg);
        assert_eq!(ansi.text, semantic.text);
        assert!(Palette::parse("background = '#000000'").is_none());
        assert!(Palette::parse("background = '#oops!!'\nforeground = '#ffffff'").is_none());
    }
    #[test]
    fn text_remains_readable_in_light_dark_and_low_contrast_themes() {
        for input in [
            DARK,
            LIGHT,
            "background = '#101010'\nforeground = '#eeeeee'\nmuted = '#161616'\naccent = '#101010'\nselection = '#eeeeee'",
            "background = '#dddddd'\nforeground = '#cccccc'\nlighter_background = '#000000'\nmuted = '#dddddd'\naccent = '#ffffff'\nselection = '#333333'",
        ] {
            let colors = Palette::parse(input).unwrap();
            for surface in [colors.bg, colors.panel, colors.selection] {
                for ink in [colors.text, colors.muted, colors.accent] {
                    assert!(
                        contrast(rgb(ink), rgb(surface)) >= 4.5,
                        "unreadable palette: {colors:?}"
                    );
                }
            }
        }
    }
    #[test]
    fn live_theme_survives_directory_replacement_and_partial_writes() {
        let fixture = Fixture::new();
        let path = fixture.write("current/theme/colors.toml", DARK);
        let mut theme = Theme::new(vec![path.clone()]);
        let dark = theme.colors;
        assert_ne!(dark, Palette::default());
        fs::rename(path.parent().unwrap(), fixture.0.join("old-theme")).unwrap();
        theme.reload();
        assert_eq!(
            theme.colors, dark,
            "keep colors while Omarchy swaps directories"
        );
        fixture.write("current/theme/colors.toml", "background = '#ffffff'");
        theme.reload();
        assert_eq!(
            theme.colors, dark,
            "incomplete file must not clear the current palette"
        );
        fixture.write("current/theme/colors.toml", LIGHT);
        theme.last_poll = Instant::now() - Duration::from_secs(2);
        theme.poll();
        assert_eq!(theme.colors, Palette::parse(LIGHT).unwrap());
        assert_eq!(theme.source(), Some(path.as_path()));
    }
    #[test]
    fn detects_a_new_install_and_prefers_modern_state_over_legacy_config() {
        let fixture = Fixture::new();
        let modern = fixture.0.join("state/colors.toml");
        let legacy = fixture.0.join("config/colors.toml");
        let mut theme = Theme::new(vec![modern.clone(), legacy.clone()]);
        assert_eq!(theme.colors, Palette::default());
        fixture.write("config/colors.toml", DARK);
        theme.reload();
        assert_eq!(theme.source(), Some(legacy.as_path()));
        fixture.write("state/colors.toml", LIGHT);
        theme.reload();
        assert_eq!(theme.source(), Some(modern.as_path()));
        assert_eq!(theme.colors.bg, Color::Rgb(255, 255, 255));
    }
    #[test]
    fn reader_and_link_picker_repaint_after_a_live_theme_change() {
        use crate::{App, Article, Counts, links};
        use ratatui::{Terminal, backend::TestBackend};
        use ratatui_image::picker::Picker;
        let fixture = Fixture::new();
        let path = fixture.write("colors.toml", DARK);
        let mut app = App::from_library(
            String::new(),
            Picker::halfblocks(),
            vec![],
            vec![],
            Counts::default(),
        );
        app.theme = Theme::new(vec![path]);
        let article = Article {
            id: 1,
            title: "A readable headline".into(),
            content: "<a href='https://example.com'>Source</a>".into(),
            ..Article::default()
        };
        app.items.push(article.clone());
        app.article = Some(article);
        app.focus = 2;
        let mut terminal = Terminal::new(TestBackend::new(100, 30)).unwrap();
        terminal.draw(|f| app.draw(f)).unwrap();
        let dark = app.theme.colors;
        assert_eq!(terminal.backend().buffer()[(0, 0)].bg, dark.bg);
        app.link_picker = Some(links::Picker::new(
            "Links".into(),
            String::new(),
            app.links.clone(),
        ));
        fixture.write("colors.toml", LIGHT);
        app.theme.last_poll = Instant::now() - Duration::from_secs(2);
        terminal.draw(|f| app.draw(f)).unwrap();
        let light = app.theme.colors;
        let buffer = terminal.backend().buffer();
        assert_eq!(buffer[(0, 0)].bg, light.bg);
        assert!(buffer.content.iter().any(|c| c.bg == light.panel));
        assert!(buffer.content.iter().any(|c| c.fg == light.accent));
        assert!(
            !buffer
                .content
                .iter()
                .any(|c| c.bg == dark.panel || c.fg == dark.accent)
        );
    }
}
