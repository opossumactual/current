//! Keyboard-first subscription management. Network requests never run in the draw loop.
use super::*;
use crossterm::event::KeyEvent;
use serde_json::{Value, json};
use std::{fs, io::Write, path::PathBuf};

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ManagedFeed {
    pub id: i64,
    pub title: String,
    pub url: String,
    pub category_id: Option<i64>,
    pub fetch_interval_min: u32,
    pub extract_fulltext: bool,
    pub paused: bool,
    pub unsubscribed: bool,
    pub last_error: String,
    pub last_success_at: Option<String>,
    pub last_fetched_at: Option<String>,
    pub next_fetch_at: String,
}
#[derive(Clone, Deserialize)]
struct Candidate {
    url: String,
    title: String,
}
#[derive(Default)]
struct Outcome {
    notice: String,
    candidates: Option<Vec<Candidate>>,
    selected: Option<i64>,
}
struct Reply {
    result: Result<Outcome>,
    library: Result<(Vec<ManagedFeed>, Vec<Category>)>,
}
#[derive(Clone)]
enum Kind {
    Text,
    Choice(Vec<(String, String)>),
    Bool,
}
#[derive(Clone)]
struct Field {
    label: &'static str,
    value: String,
    kind: Kind,
}
impl Field {
    fn text(label: &'static str, value: impl Into<String>) -> Self {
        Self {
            label,
            value: value.into(),
            kind: Kind::Text,
        }
    }
    fn choice(label: &'static str, value: String, choices: Vec<(String, String)>) -> Self {
        Self {
            label,
            value,
            kind: Kind::Choice(choices),
        }
    }
    fn boolean(label: &'static str, value: bool) -> Self {
        Self {
            label,
            value: value.to_string(),
            kind: Kind::Bool,
        }
    }
    fn display(&self) -> String {
        match &self.kind {
            Kind::Text => self.value.clone(),
            Kind::Bool => {
                if self.value == "true" {
                    "[x] Yes".into()
                } else {
                    "[ ] No".into()
                }
            }
            Kind::Choice(options) => format!(
                "‹ {} ›",
                options
                    .iter()
                    .find(|(v, _)| v == &self.value)
                    .map(|(_, name)| name.as_str())
                    .unwrap_or("Uncategorized")
            ),
        }
    }
    fn cycle(&mut self, d: isize) {
        match &self.kind {
            Kind::Choice(options) if !options.is_empty() => {
                let at = options
                    .iter()
                    .position(|(v, _)| v == &self.value)
                    .unwrap_or(0);
                let next = (at as isize + d).rem_euclid(options.len() as isize) as usize;
                self.value = options[next].0.clone();
            }
            Kind::Bool => self.value = (self.value != "true").to_string(),
            _ => {}
        }
    }
}
#[derive(Clone)]
enum Action {
    Discover,
    Subscribe,
    Edit(i64),
    NewCollection,
    RenameCollection(i64),
    Import,
    Export,
}
struct Form {
    title: &'static str,
    action: Action,
    fields: Vec<Field>,
    at: usize,
}
#[derive(Clone)]
enum Confirmation {
    Unsubscribe(i64, String),
    RemoveCollection(i64, String),
}

pub struct Manager {
    base: String,
    feeds: Vec<ManagedFeed>,
    categories: Vec<Category>,
    collections: bool,
    archived: bool,
    selected: usize,
    list_state: ListState,
    filter: String,
    searching: bool,
    form: Option<Form>,
    confirm: Option<Confirmation>,
    busy: bool,
    error: String,
    notice: String,
    rx: Receiver<Reply>,
    tx: Sender<Reply>,
    pub changed: bool,
}

/// Shared HTTP helper, including server error messages and responses with no body.
pub fn request(
    base: &str,
    method: &str,
    path: &str,
    body: Option<Value>,
    xml: Option<&str>,
) -> Result<Value> {
    let config = ureq::Agent::config_builder()
        .timeout_global(Some(Duration::from_secs(75)))
        .http_status_as_error(false)
        .build();
    let agent: ureq::Agent = config.into();
    let url = format!("{base}/api/{path}");
    let response = match method {
        "GET" => agent.get(&url).call(),
        "DELETE" => agent.delete(&url).call(),
        "PATCH" => agent.patch(&url).send_json(body.unwrap_or(json!({}))),
        _ => {
            if let Some(xml) = xml {
                agent
                    .post(&url)
                    .header("Content-Type", "text/xml")
                    .send(xml)
            } else {
                agent.post(&url).send_json(body.unwrap_or(json!({})))
            }
        }
    };
    let mut response = response.map_err(|e| e.to_string())?;
    let status = response.status();
    let text = response
        .body_mut()
        .read_to_string()
        .map_err(|e| e.to_string())?;
    if !status.is_success() {
        return Err(serde_json::from_str::<Value>(&text)
            .ok()
            .and_then(|v| v["error"].as_str().map(str::to_owned))
            .unwrap_or(format!("Request failed: {status}")));
    }
    if path == "opml/export" {
        return Ok(Value::String(text));
    }
    if text.is_empty() {
        return Ok(Value::Null);
    }
    serde_json::from_str(&text).map_err(|e| e.to_string())
}
fn library(base: &str) -> Result<(Vec<ManagedFeed>, Vec<Category>)> {
    Ok((
        get(base, "feeds?includeArchived=1")?,
        get(base, "categories")?,
    ))
}
fn path(value: &str) -> PathBuf {
    if let Some(tail) = value.strip_prefix("~/") {
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join(tail);
        }
    }
    PathBuf::from(value)
}
impl Manager {
    pub fn new(base: String, add: bool) -> Self {
        let (tx, rx) = mpsc::channel();
        let mut m = Self {
            base,
            feeds: vec![],
            categories: vec![],
            collections: false,
            archived: false,
            selected: 0,
            list_state: ListState::default(),
            filter: String::new(),
            searching: false,
            form: None,
            confirm: None,
            busy: false,
            error: String::new(),
            notice: "Choose a feed · A add · E edit · Tab collections · Esc return to reading"
                .into(),
            rx,
            tx,
            changed: false,
        };
        m.job(|_| Ok(Outcome::default()));
        if add {
            m.add()
        };
        m
    }
    fn job(&mut self, action: impl FnOnce(&str) -> Result<Outcome> + Send + 'static) {
        if self.busy {
            return;
        };
        self.busy = true;
        self.error.clear();
        let (base, tx) = (self.base.clone(), self.tx.clone());
        thread::spawn(move || {
            let result = action(&base);
            let lib = library(&base);
            let _ = tx.send(Reply {
                result,
                library: lib,
            });
        });
    }
    pub fn tick(&mut self) {
        while let Ok(reply) = self.rx.try_recv() {
            self.busy = false;
            let previous = self.current_feed().map(|f| f.id);
            match reply.library {
                Ok((feeds, categories)) => {
                    self.feeds = feeds;
                    self.categories = categories
                }
                Err(e) => self.error = e,
            }
            if let Some(id) = previous {
                if let Some(at) = self.visible().iter().position(|f| f.id == id) {
                    self.selected = at
                }
            }
            match reply.result {
                Err(e) => self.error = e,
                Ok(out) => {
                    if !out.notice.is_empty() {
                        self.notice = out.notice;
                        self.changed = true;
                        self.form = None;
                        self.confirm = None;
                    }
                    if let Some(candidates) = out.candidates {
                        if candidates.is_empty() {
                            self.error =
                                "No feeds found. Try the direct RSS or Atom address.".into()
                        } else {
                            let first = candidates[0].url.clone();
                            let choices = candidates
                                .into_iter()
                                .map(|c| {
                                    (
                                        c.url.clone(),
                                        if c.title.is_empty() { c.url } else { c.title },
                                    )
                                })
                                .collect();
                            self.form = Some(Form {
                                title: "SUBSCRIBE",
                                action: Action::Subscribe,
                                fields: vec![
                                    Field::choice("Feed", first, choices),
                                    self.collection_field(None),
                                ],
                                at: 0,
                            });
                        }
                    }
                    if let Some(id) = out.selected {
                        self.archived = false;
                        self.collections = false;
                        self.filter.clear();
                        self.selected = self.visible().iter().position(|f| f.id == id).unwrap_or(0);
                    }
                }
            }
            self.selected = self.selected.min(self.length().saturating_sub(1));
        }
    }
    fn visible(&self) -> Vec<&ManagedFeed> {
        let q = self.filter.to_lowercase();
        self.feeds
            .iter()
            .filter(|f| {
                f.unsubscribed == self.archived
                    && (f.title.to_lowercase().contains(&q) || f.url.to_lowercase().contains(&q))
            })
            .collect()
    }
    fn current_feed(&self) -> Option<ManagedFeed> {
        self.visible().get(self.selected).map(|f| (*f).clone())
    }
    fn length(&self) -> usize {
        if self.collections {
            self.categories.len()
        } else {
            self.visible().len()
        }
    }
    fn collection_field(&self, id: Option<i64>) -> Field {
        Field::choice(
            "Collection",
            id.map(|v| v.to_string()).unwrap_or_default(),
            std::iter::once((String::new(), "Uncategorized".into()))
                .chain(
                    self.categories
                        .iter()
                        .map(|c| (c.id.to_string(), c.title.clone())),
                )
                .collect(),
        )
    }
    fn add(&mut self) {
        self.form = Some(Form {
            title: "ADD A FEED",
            action: Action::Discover,
            fields: vec![Field::text("Website or feed URL", "")],
            at: 0,
        });
    }
    fn edit(&mut self) {
        if self.collections {
            if let Some(c) = self.categories.get(self.selected) {
                self.form = Some(Form {
                    title: "RENAME COLLECTION",
                    action: Action::RenameCollection(c.id),
                    fields: vec![Field::text("Collection name", c.title.clone())],
                    at: 0,
                })
            }
        } else if let Some(f) = self.current_feed() {
            self.form = Some(Form {
                title: "FEED SETTINGS",
                action: Action::Edit(f.id),
                fields: vec![
                    Field::text("Feed name", f.title),
                    Field::text("Feed URL", f.url),
                    self.collection_field(f.category_id),
                    Field::text(
                        "Check every (minutes, 5–1440)",
                        f.fetch_interval_min.to_string(),
                    ),
                    Field::boolean("Fetch full text for short entries", f.extract_fulltext),
                ],
                at: 0,
            })
        }
    }
    fn submit(&mut self) {
        let Some(form) = &self.form else { return };
        let fields = form
            .fields
            .iter()
            .map(|f| f.value.clone())
            .collect::<Vec<_>>();
        let action = form.action.clone();
        if fields[0].trim().is_empty() {
            self.error = "Enter a value first.".into();
            return;
        }
        if matches!(action, Action::Edit(_))
            && fields[3]
                .parse::<u32>()
                .ok()
                .filter(|n| (5..=1440).contains(n))
                .is_none()
        {
            self.error = "Choose a check interval between 5 and 1440 minutes.".into();
            return;
        }
        self.job(move|base|{
            let mut out=Outcome::default();
            match action {
                Action::Discover=>out.candidates=Some(serde_json::from_value(request(base,"GET",&format!("discover?url={}",encode(&fields[0])),None,None)?).map_err(|e|e.to_string())?),
                Action::Subscribe=>{let result=request(base,"POST","feeds",Some(json!({"url":fields[0],"categoryId":fields[1].parse::<i64>().ok(),"direct":true})),None)?;out.selected=result["id"].as_i64();out.notice="Subscribed. Check feed status for its first update.".into();}
                Action::Edit(id)=>{request(base,"PATCH",&format!("feeds/{id}"),Some(json!({"title":fields[0],"url":fields[1],"categoryId":fields[2].parse::<i64>().ok(),"fetchIntervalMin":fields[3].parse::<u32>().map_err(|e|e.to_string())?,"extractFulltext":fields[4]=="true"})),None)?;out.notice="Feed settings saved.".into();}
                Action::NewCollection=>{request(base,"POST","categories",Some(json!({"title":fields[0]})),None)?;out.notice="Collection created. E on a feed moves it into a collection.".into();}
                Action::RenameCollection(id)=>{request(base,"PATCH",&format!("categories/{id}"),Some(json!({"title":fields[0]})),None)?;out.notice="Collection renamed.".into();}
                Action::Import=>{
                    let file=fs::File::open(path(&fields[0])).map_err(|e|format!("Cannot open OPML: {e}"))?;
                    if file.metadata().map_err(|e|e.to_string())?.len()>8<<20{return Err("OPML files must be smaller than 8 MiB.".into())}
                    let mut xml=String::new();use std::io::Read;file.take((8<<20)+1).read_to_string(&mut xml).map_err(|e|e.to_string())?;
                    let result=request(base,"POST","opml/import",None,Some(&xml))?;out.notice=format!("Imported {} feeds; skipped {} existing subscriptions.",result["added"],result["skipped"]);
                }
                Action::Export=>{
                    let result=request(base,"GET","opml/export",None,None)?;
                    let mut file=fs::OpenOptions::new().write(true).create_new(true).open(path(&fields[0])).map_err(|e|format!("Cannot create export (choose a new filename): {e}"))?;
                    file.write_all(result.as_str().unwrap_or("").as_bytes()).map_err(|e|e.to_string())?;out.notice=format!("Subscriptions exported to {}",fields[0]);
                }
            };Ok(out)
        });
    }
    pub fn event(&mut self, key: KeyEvent) -> bool {
        if key.code == KeyCode::Esc {
            if self.searching {
                self.searching = false
            } else if self.confirm.is_some() {
                self.confirm = None
            } else if self.form.is_some() {
                self.form = None
            } else {
                return true;
            };
            return false;
        }
        if self.busy {
            return false;
        }
        if self.searching {
            match key.code {
                KeyCode::Enter => self.searching = false,
                KeyCode::Backspace => {
                    self.filter.pop();
                }
                KeyCode::Char(c) => self.filter.push(c),
                _ => {}
            }
            self.selected = 0;
            return false;
        }
        if let Some(confirm) = self.confirm.clone() {
            match key.code {
                KeyCode::Char('y') | KeyCode::Enter => self.job(move |base| {
                    let (route, notice) = match confirm {
                        Confirmation::Unsubscribe(id, _) => (
                            format!("feeds/{id}"),
                            "Unsubscribed. Your articles and saved items are kept.",
                        ),
                        Confirmation::RemoveCollection(id, _) => (
                            format!("categories/{id}"),
                            "Collection removed. Its feeds are now Uncategorized.",
                        ),
                    };
                    request(base, "DELETE", &route, None, None)?;
                    Ok(Outcome {
                        notice: notice.into(),
                        ..Default::default()
                    })
                }),
                KeyCode::Char('n') => self.confirm = None,
                _ => {}
            };
            return false;
        }
        if let Some(form) = &mut self.form {
            match key.code {
                KeyCode::Tab | KeyCode::Down => form.at = (form.at + 1) % form.fields.len(),
                KeyCode::BackTab | KeyCode::Up => {
                    form.at = (form.at + form.fields.len() - 1) % form.fields.len()
                }
                KeyCode::Enter => self.submit(),
                KeyCode::Char('s') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                    self.submit()
                }
                KeyCode::Char('u') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                    form.fields[form.at].value.clear()
                }
                KeyCode::Left => form.fields[form.at].cycle(-1),
                KeyCode::Right => form.fields[form.at].cycle(1),
                KeyCode::Char(' ') if !matches!(form.fields[form.at].kind, Kind::Text) => {
                    form.fields[form.at].cycle(1)
                }
                KeyCode::Char(c)
                    if !key
                        .modifiers
                        .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT)
                        && matches!(form.fields[form.at].kind, Kind::Text) =>
                {
                    form.fields[form.at].value.push(c)
                }
                KeyCode::Backspace if matches!(form.fields[form.at].kind, Kind::Text) => {
                    form.fields[form.at].value.pop();
                }
                _ => {}
            };
            return false;
        }
        match key.code {
            KeyCode::Char('q') => return true,
            KeyCode::Down | KeyCode::Char('j') => {
                self.selected = (self.selected + 1).min(self.length().saturating_sub(1))
            }
            KeyCode::Up | KeyCode::Char('k') => self.selected = self.selected.saturating_sub(1),
            KeyCode::PageDown => {
                self.selected = (self.selected + 10).min(self.length().saturating_sub(1))
            }
            KeyCode::PageUp => self.selected = self.selected.saturating_sub(10),
            KeyCode::Home => self.selected = 0,
            KeyCode::End => self.selected = self.length().saturating_sub(1),
            KeyCode::Tab | KeyCode::Char('c') => {
                self.collections = !self.collections;
                self.selected = 0;
                self.filter.clear()
            }
            KeyCode::Char('/') if !self.collections => {
                self.searching = true;
                self.filter.clear()
            }
            KeyCode::Char('a') => self.add(),
            KeyCode::Enter | KeyCode::Char('e') => self.edit(),
            KeyCode::Char('n') => {
                self.form = Some(Form {
                    title: "NEW COLLECTION",
                    action: Action::NewCollection,
                    fields: vec![Field::text("Collection name", "")],
                    at: 0,
                })
            }
            KeyCode::Char('i') => {
                self.form = Some(Form {
                    title: "IMPORT OPML",
                    action: Action::Import,
                    fields: vec![Field::text("OPML file path (~/ is supported)", "")],
                    at: 0,
                })
            }
            KeyCode::Char('o') => {
                self.form = Some(Form {
                    title: "EXPORT OPML",
                    action: Action::Export,
                    fields: vec![Field::text("New export file path", "~/current.opml")],
                    at: 0,
                })
            }
            KeyCode::Char('v') => {
                self.archived = !self.archived;
                self.collections = false;
                self.selected = 0
            }
            KeyCode::Char('d') => {
                if self.collections {
                    if let Some(c) = self.categories.get(self.selected) {
                        self.confirm = Some(Confirmation::RemoveCollection(c.id, c.title.clone()))
                    }
                } else if let Some(f) = self.current_feed() {
                    if !f.unsubscribed {
                        self.confirm = Some(Confirmation::Unsubscribe(f.id, f.title))
                    }
                }
            }
            KeyCode::Char('p') => {
                if !self.collections {
                    if let Some(f) = self.current_feed() {
                        self.job(move |base| {
                            let change = if f.unsubscribed {
                                json!({"unsubscribed":false,"paused":false})
                            } else {
                                json!({"paused":!f.paused})
                            };
                            request(
                                base,
                                "PATCH",
                                &format!("feeds/{}", f.id),
                                Some(change),
                                None,
                            )?;
                            Ok(Outcome {
                                notice: if f.unsubscribed {
                                    "Subscription restored."
                                } else if f.paused {
                                    "Updates resumed."
                                } else {
                                    "Updates paused."
                                }
                                .into(),
                                selected: Some(f.id),
                                ..Default::default()
                            })
                        })
                    }
                }
            }
            KeyCode::Char('r') => {
                if let Some(f) = self
                    .current_feed()
                    .filter(|f| !f.unsubscribed && !self.collections)
                {
                    self.job(move |base| {
                        let result =
                            request(base, "POST", &format!("feeds/{}/refresh", f.id), None, None)?;
                        let error = result["feed"]["lastError"].as_str().unwrap_or("");
                        Ok(Outcome {
                            notice: if error.is_empty() {
                                format!("Updated. {} new articles.", result["inserted"])
                            } else {
                                format!("Update failed: {error}")
                            },
                            ..Default::default()
                        })
                    })
                } else {
                    self.job(|_| Ok(Outcome::default()))
                }
            }
            _ => {}
        };
        false
    }
    pub fn draw(&mut self, f: &mut Frame, colors: Palette) {
        let area = f.area();
        f.render_widget(Clear, area);
        f.render_widget(
            Block::default().style(Style::default().bg(colors.bg).fg(colors.text)),
            area,
        );
        let rows = Layout::vertical([
            Constraint::Length(3),
            Constraint::Min(5),
            Constraint::Length(4),
            Constraint::Length(3),
        ])
        .margin(1)
        .split(area);
        f.render_widget(
            Paragraph::new(Line::from(vec![
                Span::styled(
                    "◉ current  /  YOUR SOURCES",
                    Style::default().fg(colors.accent).bold(),
                ),
                Span::styled(
                    if self.busy {
                        "    Working…"
                    } else {
                        "    Esc · back to reading"
                    },
                    Style::default().fg(colors.muted),
                ),
            ]))
            .block(
                Block::default()
                    .borders(Borders::BOTTOM)
                    .border_style(Style::default().fg(colors.line)),
            ),
            rows[0],
        );
        if let Some(form) = &self.form {
            let block = panel(form.title, true, colors);
            let inner = block.inner(rows[1]);
            f.render_widget(block, rows[1]);
            let count = (inner.height as usize / 3).max(1);
            let start = form.at.saturating_sub(count - 1);
            for (i, field) in form.fields.iter().enumerate().skip(start).take(count) {
                let r = Rect::new(
                    inner.x + 1,
                    inner.y + ((i - start) * 3) as u16,
                    inner.width.saturating_sub(2),
                    3,
                );
                let selected = i == form.at;
                let display = clean(&field.display());
                let content = if selected && matches!(field.kind, Kind::Text) {
                    format!("{display}▏")
                } else {
                    display
                };
                let value = Paragraph::new(content.clone())
                    .style(Style::default().fg(if selected { colors.text } else { colors.muted }));
                f.render_widget(
                    value
                        .block(
                            Block::default()
                                .borders(Borders::ALL)
                                .border_style(Style::default().fg(if selected {
                                    colors.accent
                                } else {
                                    colors.line
                                }))
                                .title(field.label),
                        )
                        .scroll((
                            0,
                            if selected {
                                content
                                    .chars()
                                    .count()
                                    .saturating_sub(r.width.saturating_sub(4) as usize)
                                    .min(u16::MAX as usize) as u16
                            } else {
                                0
                            },
                        )),
                    r,
                );
            }
        } else if let Some(confirm) = &self.confirm {
            let text = match confirm {
                Confirmation::Unsubscribe(_, name) => format!(
                    "Unsubscribe from {}?\n\nYour articles and saved items will remain in the library.\nYou can resubscribe later from V · unsubscribed.\n\nY / Enter confirms · N / Esc cancels",
                    clean(name)
                ),
                Confirmation::RemoveCollection(_, name) => format!(
                    "Remove collection {}?\n\nIts feeds will move to Uncategorized. All articles are kept.\n\nY / Enter confirms · N / Esc cancels",
                    clean(name)
                ),
            };
            f.render_widget(
                Paragraph::new(text).wrap(Wrap { trim: false }).block(panel(
                    " CONFIRM ",
                    true,
                    colors,
                )),
                rows[1],
            );
        } else {
            let parts =
                Layout::horizontal([Constraint::Percentage(43), Constraint::Percentage(57)])
                    .split(rows[1]);
            let entries = if self.collections {
                self.categories
                    .iter()
                    .map(|c| ListItem::new(format!("  {}", clean(&c.title))))
                    .collect::<Vec<_>>()
            } else {
                self.visible()
                    .iter()
                    .map(|feed| {
                        ListItem::new(vec![
                            Line::from(clean(if feed.title.is_empty() {
                                &feed.url
                            } else {
                                &feed.title
                            })),
                            Line::from(Span::styled(
                                if feed.unsubscribed {
                                    "  Unsubscribed"
                                } else if feed.paused {
                                    "  Paused"
                                } else if !feed.last_error.is_empty() {
                                    "  Needs attention"
                                } else if feed.last_success_at.is_some() {
                                    "  Up to date"
                                } else {
                                    "  Awaiting first update"
                                },
                                Style::default().fg(colors.muted),
                            )),
                        ])
                    })
                    .collect()
            };
            let title = if self.collections {
                " COLLECTIONS · Tab feeds ".into()
            } else if self.searching || !self.filter.is_empty() {
                format!(
                    " / {}{} ",
                    clean(&self.filter),
                    if self.searching { "▏" } else { "" }
                )
            } else if self.archived {
                " UNSUBSCRIBED · V active ".into()
            } else {
                " FEEDS · Tab collections ".into()
            };
            self.list_state.select(Some(self.selected));
            f.render_stateful_widget(
                List::new(entries)
                    .block(panel(&title, true, colors))
                    .highlight_style(Style::default().bg(colors.selection).fg(colors.accent))
                    .highlight_symbol("▎"),
                parts[0],
                &mut self.list_state,
            );
            let details = if self.collections {
                self.categories.get(self.selected).map(|c|format!("{}\n\n{} subscriptions\n\nE  Rename collection\nD  Remove collection\nN  New collection\n\nTo move a feed here, switch to feeds with Tab, select it and press E.",clean(&c.title),self.feeds.iter().filter(|f|f.category_id==Some(c.id)&&!f.unsubscribed).count())).unwrap_or("Create your first collection with N.".into())
            } else if let Some(feed) = self.current_feed() {
                format!(
                    "{}\n\n{}\n\nCollection: {}\nCheck every: {} minutes\nFull text: {}\n\nLast attempt: {}\nLast success: {}\nNext check: {}\n\n{}\n\nE  Edit name, URL, collection and schedule\nP  {}\nR  Update now\nD  Unsubscribe (keeps articles)",
                    clean(&feed.title),
                    clean(&feed.url),
                    self.categories
                        .iter()
                        .find(|c| Some(c.id) == feed.category_id)
                        .map(|c| clean(&c.title))
                        .unwrap_or("Uncategorized".into()),
                    feed.fetch_interval_min,
                    if feed.extract_fulltext { "On" } else { "Off" },
                    feed.last_fetched_at.as_deref().unwrap_or("Not yet"),
                    feed.last_success_at.as_deref().unwrap_or("Not yet"),
                    if feed.paused || feed.unsubscribed {
                        "Off"
                    } else if feed.next_fetch_at.starts_with("1970") {
                        "Queued"
                    } else {
                        &feed.next_fetch_at
                    },
                    clean(&feed.last_error),
                    if feed.unsubscribed {
                        "Resubscribe"
                    } else if feed.paused {
                        "Resume updates"
                    } else {
                        "Pause updates"
                    }
                )
            } else {
                "Your reading room starts here.\n\nA  Add a website or feed URL\nI  Import an OPML subscription file\nN  Create a collection\n\nOne library, shared with the graphical reader.".into()
            };
            f.render_widget(
                Paragraph::new(details)
                    .wrap(Wrap { trim: false })
                    .block(panel(" DETAILS ", false, colors)),
                parts[1],
            );
        }
        f.render_widget(
            Paragraph::new(clean(if !self.error.is_empty() {
                &self.error
            } else {
                &self.notice
            }))
            .style(Style::default().fg(if self.error.is_empty() {
                colors.muted
            } else {
                colors.accent
            }))
            .wrap(Wrap { trim: false }),
            rows[2],
        );
        f.render_widget(Paragraph::new(if self.form.is_some(){"Tab / ↑↓ fields    ←→ / Space choices    Ctrl+U clear field\nEnter / Ctrl+S submit    Esc cancel"}else{"A add  E edit  P pause/resume  R update  D unsubscribe  / find\nTab collections  N new collection  I import  O export  V unsubscribed"}).style(Style::default().fg(colors.accent)),rows[3]);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn key(manager: &mut Manager, code: KeyCode) {
        manager.event(KeyEvent::new(code, KeyModifiers::NONE));
    }
    fn wait(manager: &mut Manager) {
        let deadline = Instant::now() + Duration::from_secs(15);
        while manager.busy && Instant::now() < deadline {
            manager.tick();
            thread::sleep(Duration::from_millis(20));
        }
        assert!(!manager.busy, "management request timed out");
        assert!(manager.error.is_empty(), "{}", manager.error);
    }
    #[test]
    #[ignore = "run by the isolated browser/integration harness"]
    fn management_round_trip() {
        let base = std::env::var("CURRENT_TEST_URL").expect("isolated test server");
        let fixture = std::env::var("CURRENT_TEST_FEED").expect("test feed URL");
        let mut manager = Manager::new(base.clone(), false);
        wait(&mut manager);
        key(&mut manager, KeyCode::Char('n'));
        manager.form.as_mut().unwrap().fields[0].value = "Terminal collection".into();
        key(&mut manager, KeyCode::Enter);
        wait(&mut manager);
        let cat = manager
            .categories
            .iter()
            .find(|c| c.title == "Terminal collection")
            .unwrap()
            .id;
        key(&mut manager, KeyCode::Char('a'));
        manager.form.as_mut().unwrap().fields[0].value = fixture;
        key(&mut manager, KeyCode::Enter);
        wait(&mut manager);
        assert!(matches!(
            manager.form.as_ref().unwrap().action,
            Action::Subscribe
        ));
        key(&mut manager, KeyCode::Tab);
        manager.form.as_mut().unwrap().fields[1].value = cat.to_string();
        key(&mut manager, KeyCode::Enter);
        wait(&mut manager);
        let feed = manager.current_feed().unwrap();
        assert_eq!(feed.category_id, Some(cat));
        key(&mut manager, KeyCode::Char('e'));
        let fields = &mut manager.form.as_mut().unwrap().fields;
        fields[0].value = "Terminal renamed".into();
        fields[3].value = "15".into();
        fields[4].value = "true".into();
        key(&mut manager, KeyCode::Enter);
        wait(&mut manager);
        assert_eq!(manager.current_feed().unwrap().title, "Terminal renamed");
        assert_eq!(manager.current_feed().unwrap().fetch_interval_min, 15);
        key(&mut manager, KeyCode::Char('p'));
        wait(&mut manager);
        assert!(manager.current_feed().unwrap().paused);
        let page: Page = get(&base, &format!("articles?feed={}", feed.id)).unwrap();
        assert!(!page.items.is_empty());
        let article = page.items[0].id;
        request(
            &base,
            "PATCH",
            &format!("articles/{article}"),
            Some(json!({"starred":true})),
            None,
        )
        .unwrap();
        key(&mut manager, KeyCode::Char('d'));
        key(&mut manager, KeyCode::Char('y'));
        wait(&mut manager);
        let preserved: Article = get(&base, &format!("articles/{article}")).unwrap();
        assert!(preserved.starred);
        key(&mut manager, KeyCode::Char('v'));
        manager.filter = "Terminal renamed".into();
        manager.selected = 0;
        assert!(manager.current_feed().unwrap().unsubscribed);
        key(&mut manager, KeyCode::Char('p'));
        wait(&mut manager);
        assert!(!manager.current_feed().unwrap().unsubscribed);
        assert!(!manager.current_feed().unwrap().paused);
        let file = std::env::temp_dir().join(format!("current-test-{}.opml", std::process::id()));
        key(&mut manager, KeyCode::Char('o'));
        manager.form.as_mut().unwrap().fields[0].value = file.display().to_string();
        key(&mut manager, KeyCode::Enter);
        wait(&mut manager);
        assert!(
            fs::read_to_string(&file)
                .unwrap()
                .contains("Terminal renamed")
        );
        key(&mut manager, KeyCode::Char('i'));
        manager.form.as_mut().unwrap().fields[0].value = file.display().to_string();
        key(&mut manager, KeyCode::Enter);
        wait(&mut manager);
        fs::remove_file(file).unwrap();
        let mut terminal = Terminal::new(ratatui::backend::TestBackend::new(110, 35)).unwrap();
        terminal
            .draw(|frame| manager.draw(frame, Palette::default()))
            .unwrap();
        assert!(manager.changed);
    }
}
