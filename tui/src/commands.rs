use crate::{Palette, layout, panel};
use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use ratatui::{
    prelude::*,
    widgets::{Clear, List, ListItem, ListState, Paragraph, Wrap},
};

#[derive(Clone)]
pub struct Command {
    pub title: &'static str,
    pub hint: &'static str,
    pub shortcut: &'static str,
    pub key: KeyEvent,
    pub disabled: Option<&'static str>,
}
impl Command {
    pub fn new(
        title: &'static str,
        hint: &'static str,
        shortcut: &'static str,
        code: KeyCode,
    ) -> Self {
        Self {
            title,
            hint,
            shortcut,
            key: KeyEvent::new(code, KeyModifiers::NONE),
            disabled: None,
        }
    }
    pub fn needs(mut self, enabled: bool, reason: &'static str) -> Self {
        if !enabled {
            self.disabled = Some(reason);
        }
        self
    }
}
pub struct Menu {
    pub query: String,
    commands: Vec<Command>,
    selected: usize,
    state: ListState,
}
pub enum Action {
    Stay,
    Close,
    Run(KeyEvent),
}
impl Menu {
    pub fn new(commands: Vec<Command>) -> Self {
        Self {
            query: String::new(),
            commands,
            selected: 0,
            state: ListState::default(),
        }
    }
    fn matches(&self) -> Vec<usize> {
        let words = self.query.to_lowercase();
        let mut matches = self
            .commands
            .iter()
            .enumerate()
            .filter_map(|(i, c)| {
                let hay = format!("{} {} {}", c.title, c.hint, c.shortcut).to_lowercase();
                words
                    .split_whitespace()
                    .all(|word| {
                        let mut chars = hay.chars();
                        word.chars()
                            .all(|ch| chars.any(|candidate| candidate == ch))
                    })
                    .then_some(i)
            })
            .collect::<Vec<_>>();
        matches.sort_by_key(|&i| {
            (
                !self.commands[i].title.to_lowercase().contains(words.trim()),
                self.commands[i].disabled.is_some(),
            )
        });
        matches
    }
    pub fn key(&mut self, key: KeyEvent) -> Action {
        let count = self.matches().len();
        match key.code {
            KeyCode::Esc => return Action::Close,
            KeyCode::Char('p') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                return Action::Close;
            }
            KeyCode::Down => self.selected = (self.selected + 1).min(count.saturating_sub(1)),
            KeyCode::Up => self.selected = self.selected.saturating_sub(1),
            KeyCode::PageDown => self.selected = (self.selected + 8).min(count.saturating_sub(1)),
            KeyCode::PageUp => self.selected = self.selected.saturating_sub(8),
            KeyCode::Enter => {
                if let Some(&i) = self.matches().get(self.selected) {
                    if self.commands[i].disabled.is_none() {
                        return Action::Run(self.commands[i].key);
                    }
                }
            }
            KeyCode::Backspace => {
                self.query.pop();
                self.selected = 0;
            }
            KeyCode::Char(c)
                if !key
                    .modifiers
                    .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT) =>
            {
                self.query.push(c);
                self.selected = 0;
            }
            _ => {}
        }
        Action::Stay
    }
    pub fn draw(&mut self, f: &mut Frame, colors: Palette) {
        let area = f.area();
        let popup = Rect::new(
            area.x + area.width.saturating_sub(78) / 2,
            area.y + area.height.saturating_sub(23) / 2,
            area.width.min(78),
            area.height.min(23),
        );
        let block = panel(" COMMANDS ", true, colors);
        let parts = Layout::vertical([
            Constraint::Length(2),
            Constraint::Min(2),
            Constraint::Length(3),
            Constraint::Length(1),
        ])
        .split(block.inner(popup).inner(Margin::new(1, 0)));
        f.render_widget(Clear, popup);
        f.render_widget(block, popup);
        f.render_widget(
            Paragraph::new(format!(
                "> {}▏",
                layout::tail(&self.query, parts[0].width.saturating_sub(3) as usize)
            ))
            .style(Style::default().fg(colors.text)),
            parts[0],
        );
        let matches = self.matches();
        let rows = matches
            .iter()
            .map(|&i| {
                let c = &self.commands[i];
                let max = parts[1].width.saturating_sub(c.shortcut.len() as u16 + 4) as usize;
                let title = layout::ellipsis(c.title, max);
                ListItem::new(format!(
                    "{}{}{}",
                    title,
                    " ".repeat(max.saturating_sub(layout::width(&title)) + 2),
                    c.shortcut
                ))
                .style(Style::default().fg(if c.disabled.is_some() {
                    colors.muted
                } else {
                    colors.text
                }))
            })
            .collect::<Vec<_>>();
        self.state
            .select((!matches.is_empty()).then_some(self.selected));
        f.render_stateful_widget(
            List::new(rows)
                .highlight_symbol("› ")
                .highlight_style(Style::default().bg(colors.selection)),
            parts[1],
            &mut self.state,
        );
        let hint = matches
            .get(self.selected)
            .map(|&i| self.commands[i].disabled.unwrap_or(self.commands[i].hint))
            .unwrap_or("No matching commands. Try refresh, image, feeds, or read.");
        f.render_widget(
            Paragraph::new(hint)
                .wrap(Wrap { trim: false })
                .style(Style::default().fg(colors.muted)),
            parts[2],
        );
        f.render_widget(
            Paragraph::new("↑/↓ select · Enter run · Esc close")
                .style(Style::default().fg(colors.accent)),
            parts[3],
        );
    }
}
