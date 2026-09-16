use crate::{Palette, Result, clean, panel};
use crossterm::event::KeyCode;
use html2text::render::{PlainDecorator, TaggedLine, TextDecorator};
use ratatui::{
    prelude::*,
    widgets::{Clear, List, ListItem, ListState, Paragraph, Wrap},
};
use std::{cell::RefCell, rc::Rc};
use url::Url;

/// Capture the renderer's own footnote table, not apparent "[n]:" text in an
/// article. This keeps numbering exact across nested blocks and wrapped URLs.
#[derive(Clone)]
struct LinkDecorator {
    plain: PlainDecorator,
    links: Rc<RefCell<Vec<String>>>,
}
impl TextDecorator for LinkDecorator {
    type Annotation = ();
    fn decorate_link_start(&mut self, url: &str) -> (String, ()) {
        self.plain.decorate_link_start(url)
    }
    fn decorate_link_end(&mut self) -> String {
        self.plain.decorate_link_end()
    }
    fn decorate_em_start(&self) -> (String, ()) {
        self.plain.decorate_em_start()
    }
    fn decorate_em_end(&self) -> String {
        self.plain.decorate_em_end()
    }
    fn decorate_strong_start(&self) -> (String, ()) {
        self.plain.decorate_strong_start()
    }
    fn decorate_strong_end(&self) -> String {
        self.plain.decorate_strong_end()
    }
    fn decorate_strikeout_start(&self) -> (String, ()) {
        self.plain.decorate_strikeout_start()
    }
    fn decorate_strikeout_end(&self) -> String {
        self.plain.decorate_strikeout_end()
    }
    fn decorate_code_start(&self) -> (String, ()) {
        self.plain.decorate_code_start()
    }
    fn decorate_code_end(&self) -> String {
        self.plain.decorate_code_end()
    }
    fn decorate_preformat_first(&self) {
        self.plain.decorate_preformat_first()
    }
    fn decorate_preformat_cont(&self) {
        self.plain.decorate_preformat_cont()
    }
    fn decorate_image(&mut self, src: &str, title: &str) -> (String, ()) {
        self.plain.decorate_image(src, title)
    }
    fn header_prefix(&self, level: usize) -> String {
        self.plain.header_prefix(level)
    }
    fn quote_prefix(&self) -> String {
        self.plain.quote_prefix()
    }
    fn unordered_item_prefix(&self) -> String {
        self.plain.unordered_item_prefix()
    }
    fn ordered_item_prefix(&self, i: i64) -> String {
        self.plain.ordered_item_prefix(i)
    }
    fn make_subblock_decorator(&self) -> Self {
        self.clone()
    }
    fn finalise(&mut self, urls: Vec<String>) -> Vec<TaggedLine<()>> {
        *self.links.borrow_mut() = urls.clone();
        self.plain.finalise(urls)
    }
}

pub fn render(html: &str, width: usize) -> Result<(String, Vec<String>)> {
    let links = Rc::new(RefCell::new(Vec::new()));
    let decorator = LinkDecorator {
        plain: PlainDecorator::new(),
        links: links.clone(),
    };
    let text = html2text::config::with_decorator(decorator)
        .do_decorate()
        .link_footnotes(true)
        .string_from_read(html.as_bytes(), width)
        .map_err(|e| e.to_string())?;
    let urls = links.borrow().clone();
    Ok((clean(&text), urls))
}

pub fn web_url(raw: &str, base: &str) -> Result<String> {
    if raw.chars().any(char::is_control) {
        return Err("This link contains invalid characters.".into());
    }
    let url = Url::parse(raw)
        .or_else(|_| Url::parse(base)?.join(raw))
        .map_err(|_| "This link has no valid web address.".to_string())?;
    if !matches!(url.scheme(), "http" | "https") || url.host_str().is_none() {
        return Err("Only http and https links can be opened here.".into());
    }
    Ok(url.to_string())
}

#[derive(Debug, PartialEq)]
pub enum Action {
    Stay,
    Close,
    Open(String),
}

pub struct Picker {
    title: String,
    base: String,
    urls: Vec<String>,
    selected: usize,
    state: ListState,
    number: String,
    error: String,
}
impl Picker {
    pub fn new(title: String, base: String, urls: Vec<String>) -> Self {
        Self {
            title,
            base,
            urls,
            selected: 0,
            state: ListState::default(),
            number: String::new(),
            error: String::new(),
        }
    }
    fn typed_index(&self) -> Option<usize> {
        self.number
            .parse::<usize>()
            .ok()?
            .checked_sub(1)
            .filter(|&n| n < self.urls.len())
    }
    pub fn key(&mut self, key: KeyCode) -> Action {
        self.error.clear();
        match key {
            KeyCode::Esc | KeyCode::Char('q' | 'b') => return Action::Close,
            KeyCode::Char(c) if c.is_ascii_digit() => {
                if self.number.len() < 8 {
                    self.number.push(c);
                }
                if let Some(n) = self.typed_index() {
                    self.selected = n;
                }
            }
            KeyCode::Backspace => {
                self.number.pop();
                if let Some(n) = self.typed_index() {
                    self.selected = n;
                }
            }
            KeyCode::Char('j') | KeyCode::Down => {
                self.number.clear();
                self.selected = (self.selected + 1).min(self.urls.len().saturating_sub(1));
            }
            KeyCode::Char('k') | KeyCode::Up => {
                self.number.clear();
                self.selected = self.selected.saturating_sub(1);
            }
            KeyCode::Enter => {
                let index = if self.number.is_empty() {
                    Some(self.selected)
                } else {
                    self.typed_index()
                };
                if let Some(url) = index.and_then(|i| self.urls.get(i)) {
                    match web_url(url, &self.base) {
                        Ok(url) => return Action::Open(url),
                        Err(error) => self.error = error,
                    }
                } else {
                    self.error = format!("Choose a link number from 1 to {}.", self.urls.len());
                }
            }
            _ => {}
        }
        Action::Stay
    }
    pub fn draw(&mut self, f: &mut Frame, colors: Palette) {
        let area = f.area();
        let width = area.width.saturating_sub(4).min(96);
        let height = area.height.saturating_sub(2).min(24);
        let popup = Rect::new(
            area.x + (area.width - width) / 2,
            area.y + (area.height - height) / 2,
            width,
            height,
        );
        let block = panel(" ARTICLE LINKS ", true, colors);
        let inner = block.inner(popup).inner(Margin::new(1, 0));
        f.render_widget(Clear, popup);
        f.render_widget(block, popup);
        let parts = Layout::vertical([
            Constraint::Length(2),
            Constraint::Min(2),
            Constraint::Length(3),
            Constraint::Length(2),
        ])
        .split(inner);
        f.render_widget(
            Paragraph::new(clean(&self.title)).style(Style::default().fg(colors.text).bold()),
            parts[0],
        );
        let rows = self
            .urls
            .iter()
            .enumerate()
            .map(|(n, url)| ListItem::new(format!("[{}] {}", n + 1, clean(url))))
            .collect::<Vec<_>>();
        self.state.select(Some(self.selected));
        f.render_stateful_widget(
            List::new(rows)
                .style(Style::default().fg(colors.text).bg(colors.panel))
                .highlight_style(
                    Style::default()
                        .bg(colors.selection)
                        .fg(colors.accent)
                        .bold(),
                )
                .highlight_symbol("› "),
            parts[1],
            &mut self.state,
        );
        let destination = self
            .urls
            .get(self.selected)
            .map(|url| web_url(url, &self.base).unwrap_or_else(|_| clean(url)))
            .unwrap_or_default();
        f.render_widget(
            Paragraph::new(destination)
                .wrap(Wrap { trim: false })
                .style(Style::default().fg(colors.muted)),
            parts[2],
        );
        f.render_widget(
            Paragraph::new(vec![
                Line::styled(
                    format!(
                        "Number: {}▏  j/k select · Enter open · Esc back",
                        self.number
                    ),
                    Style::default().fg(colors.accent),
                ),
                Line::styled(
                    if self.error.is_empty() {
                        "Numbers match the references in the article."
                    } else {
                        &self.error
                    },
                    Style::default().fg(colors.muted),
                ),
            ]),
            parts[3],
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn footnotes_match_the_existing_renderer_even_in_nested_blocks() {
        let html = r#"<p><a href="https://example.com/first?a=1&amp;b=2">First</a></p>
            <blockquote><a href="https://example.com/nested">Nested</a></blockquote>
            <ul><li><a href="https://example.com/first?a=1&amp;b=2">Repeated</a></li></ul>
            <table><tr><td><a href="https://example.com/table">Table</a></td></tr></table>
            <p>[1]: https://unrelated.example/fake-reference</p>"#;
        for width in [12, 40, 100] {
            let (text, urls) = render(html, width).unwrap();
            assert_eq!(
                text,
                clean(&html2text::from_read(html.as_bytes(), width).unwrap())
            );
            assert_eq!(
                urls,
                [
                    "https://example.com/first?a=1&b=2",
                    "https://example.com/nested",
                    "https://example.com/first?a=1&b=2",
                    "https://example.com/table"
                ]
            );
        }
    }
    #[test]
    fn picker_handles_multi_digit_numbers_navigation_and_cancel() {
        let mut picker = Picker::new(
            "Story".into(),
            "https://example.com/story".into(),
            (1..=12).map(|n| format!("/link/{n}")).collect(),
        );
        assert_eq!(picker.key(KeyCode::Char('1')), Action::Stay);
        assert_eq!(picker.key(KeyCode::Char('2')), Action::Stay);
        assert_eq!(
            picker.key(KeyCode::Enter),
            Action::Open("https://example.com/link/12".into())
        );
        picker.key(KeyCode::Backspace);
        assert_eq!(
            picker.key(KeyCode::Enter),
            Action::Open("https://example.com/link/1".into())
        );
        picker.key(KeyCode::Down);
        assert_eq!(
            picker.key(KeyCode::Enter),
            Action::Open("https://example.com/link/2".into())
        );
        picker.key(KeyCode::Char('9'));
        picker.key(KeyCode::Char('9'));
        assert_eq!(picker.key(KeyCode::Enter), Action::Stay);
        assert!(picker.error.contains("1 to 12"));
        assert_eq!(picker.key(KeyCode::Esc), Action::Close);
    }
    #[test]
    fn only_web_urls_are_launched_and_relative_references_are_resolved() {
        let base = "https://example.com/articles/story";
        assert_eq!(
            web_url("../other?q=one&x=2", base).unwrap(),
            "https://example.com/other?q=one&x=2"
        );
        assert_eq!(
            web_url("#section", base).unwrap(),
            "https://example.com/articles/story#section"
        );
        assert_eq!(
            web_url("//cdn.example.com/file.pdf", base).unwrap(),
            "https://cdn.example.com/file.pdf"
        );
        for raw in [
            "javascript:alert(1)",
            "file:///etc/passwd",
            "data:text/html,test",
            "mailto:me@example.com",
            "https://example.com/\ncommand",
        ] {
            assert!(web_url(raw, base).is_err());
        }
        let mut picker = Picker::new(
            "Story".into(),
            base.into(),
            vec![
                "mailto:me@example.com".into(),
                "https://example.com/second".into(),
            ],
        );
        assert_eq!(picker.key(KeyCode::Enter), Action::Stay);
        picker.key(KeyCode::Char('2'));
        assert_eq!(
            picker.key(KeyCode::Enter),
            Action::Open("https://example.com/second".into())
        );
    }
}
