use ratatui::prelude::*;
use unicode_segmentation::UnicodeSegmentation;
use unicode_width::UnicodeWidthStr;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub enum Mode {
    #[default]
    Wide,
    Medium,
    Thin,
}
impl Mode {
    pub fn for_width(width: u16) -> Self {
        match width {
            170.. => Self::Wide,
            110.. => Self::Medium,
            _ => Self::Thin,
        }
    }
    /// Rectangles are source picker, headlines, and reader, respectively.
    /// Hidden panes have no rectangle and must not render or consume input.
    pub fn panes(self, area: Rect, focus: usize, zen: bool) -> [Option<Rect>; 3] {
        if focus == 0 && (self != Self::Wide || zen) {
            return [Some(area), None, None];
        }
        if zen || (self == Self::Thin && focus == 2) {
            return [None, None, Some(area)];
        }
        match self {
            Self::Thin => [None, Some(area), None],
            Self::Medium => {
                let parts = Layout::horizontal([Constraint::Percentage(36), Constraint::Min(64)])
                    .split(area);
                [None, Some(parts[0]), Some(parts[1])]
            }
            Self::Wide => {
                let list =
                    ((u32::from(area.width.saturating_sub(30))) * 35 / 100).clamp(44, 65) as u16;
                let parts = Layout::horizontal([
                    Constraint::Length(30),
                    Constraint::Length(list),
                    Constraint::Min(64),
                ])
                .split(area);
                [Some(parts[0]), Some(parts[1]), Some(parts[2])]
            }
        }
    }
}

pub fn width(text: &str) -> usize {
    UnicodeWidthStr::width(text)
}

pub fn ellipsis(text: &str, max: usize) -> String {
    if width(text) <= max {
        return text.into();
    }
    if max == 0 {
        return String::new();
    }
    let mut out = String::new();
    let mut used = 0;
    for grapheme in text.graphemes(true) {
        let len = width(grapheme);
        if used + len >= max {
            break;
        }
        out.push_str(grapheme);
        used += len;
    }
    out.push('…');
    out
}

pub fn tail(text: &str, max: usize) -> String {
    if width(text) <= max {
        return text.into();
    }
    if max == 0 {
        return String::new();
    }
    let mut used = 0;
    let mut start = text.len();
    for (index, grapheme) in text.grapheme_indices(true).rev() {
        let len = width(grapheme);
        if used + len >= max {
            break;
        }
        start = index;
        used += len;
    }
    format!("…{}", &text[start..])
}

pub fn wrap(text: &str, max: usize) -> String {
    let max = max.max(2);
    let mut out = String::new();
    let mut used = 0;
    for word in text.split_whitespace() {
        if used > 0 {
            if used + 1 + width(word) > max {
                out.push('\n');
                used = 0;
            } else {
                out.push(' ');
                used += 1;
            }
        }
        for grapheme in word.graphemes(true) {
            let len = width(grapheme);
            if used > 0 && used + len > max {
                out.push('\n');
                used = 0;
            }
            out.push_str(grapheme);
            used += len;
        }
    }
    out
}

pub fn title_lines(text: &str, max: usize, lines: usize) -> Vec<String> {
    let wrapped = wrap(text, max);
    let mut rows = wrapped
        .lines()
        .take(lines)
        .map(str::to_owned)
        .collect::<Vec<_>>();
    if wrapped.lines().count() > lines {
        if let Some(last) = rows.last_mut() {
            *last = format!(
                "{}…",
                ellipsis(last, max.saturating_sub(1)).trim_end_matches('…')
            );
        }
    }
    rows
}

/// A content offset survives text reflow; raw terminal row numbers do not.
pub fn content_offset(text: &str, scroll: u16) -> usize {
    text.lines()
        .take(scroll as usize)
        .flat_map(str::chars)
        .filter(|c| !c.is_whitespace())
        .count()
}
pub fn scroll_for_offset(text: &str, offset: usize) -> u16 {
    if offset == 0 {
        return 0;
    }
    let mut used = 0;
    for (row, line) in text.lines().enumerate() {
        let end = used + line.chars().filter(|c| !c.is_whitespace()).count();
        if end > offset {
            return row.min(u16::MAX as usize) as u16;
        }
        used = end;
    }
    text.lines()
        .count()
        .saturating_sub(1)
        .min(u16::MAX as usize) as u16
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn panes_fit_each_breakpoint_and_sources_remain_accessible() {
        for (columns, count) in [
            (65, 1),
            (80, 1),
            (109, 1),
            (110, 2),
            (157, 2),
            (169, 2),
            (170, 3),
            (220, 3),
        ] {
            let area = Rect::new(0, 2, columns, 40);
            let mode = Mode::for_width(columns);
            let parts = mode.panes(area, 1, false);
            assert_eq!(parts.iter().flatten().count(), count);
            let mut x = 0;
            for rect in parts.iter().flatten() {
                assert_eq!(rect.x, x, "panes must be contiguous");
                assert_eq!(rect.y, 2);
                assert_eq!(rect.height, 40);
                x += rect.width;
            }
            assert_eq!(x, columns);
            if let Some(reader) = parts[2] {
                assert!(reader.width >= 64);
            }
            if count < 3 {
                assert_eq!(mode.panes(area, 0, false), [Some(area), None, None]);
            }
            if count == 1 {
                assert_eq!(mode.panes(area, 2, false), [None, None, Some(area)]);
            }
        }
    }

    #[test]
    fn unicode_labels_and_long_words_fit_without_splitting_graphemes() {
        for text in [
            "A publisher with a very long name",
            "東京からのニュース",
            "cafe\u{301} and 👩‍💻 technology",
        ] {
            for max in 0..40 {
                assert!(width(&ellipsis(text, max)) <= max);
                assert!(width(&tail(text, max)) <= max);
            }
            for max in 2..40 {
                let wrapped = wrap(text, max);
                assert!(wrapped.lines().all(|line| width(line) <= max));
                assert_eq!(
                    wrapped
                        .chars()
                        .filter(|c| !c.is_whitespace())
                        .collect::<String>(),
                    text.chars()
                        .filter(|c| !c.is_whitespace())
                        .collect::<String>()
                );
            }
        }
        assert_eq!(ellipsis("cafe\u{301} long name", 5), "cafe\u{301}…");
        let title = title_lines("A very long headline with more words than will fit", 12, 2);
        assert_eq!(title.len(), 2);
        assert!(title[1].ends_with('…'));
        assert!(title.iter().all(|line| width(line) <= 12));
    }
}
