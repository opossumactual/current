mod actions;
mod commands;
mod gallery;
mod headline;
mod layout;
mod links;
mod manage;
#[cfg(test)]
mod reading_tests;
mod refresh;
mod theme;
mod view;
use crossterm::event::{self, Event, KeyCode, KeyEventKind, KeyModifiers};
use ratatui::{
    prelude::*,
    widgets::{Block, Borders, Clear, List, ListItem, ListState, Paragraph, Wrap},
};
use ratatui_image::{Resize, StatefulImage, picker::Picker, protocol::StatefulProtocol};
use serde::{Deserialize, Serialize};
use std::{
    collections::HashMap,
    io,
    sync::mpsc::{self, Receiver, Sender},
    thread,
    time::{Duration, Instant},
};
use theme::Palette;
type Result<T> = std::result::Result<T, String>;
const READ_DELAY: Duration = Duration::from_secs(1);
#[derive(Clone, Default, Deserialize, Serialize, Debug)]
#[serde(rename_all = "camelCase")]
struct Article {
    id: i64,
    feed_id: i64,
    #[serde(default)]
    feed_title: String,
    title: String,
    summary: String,
    #[serde(default)]
    content: String,
    #[serde(default)]
    fulltext: String,
    image_url: String,
    url: String,
    author: String,
    published_at: String,
    read: bool,
    starred: bool,
}
#[derive(Clone, Deserialize)]
struct Category {
    id: i64,
    title: String,
}
#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Feed {
    id: i64,
    title: String,
    category_id: Option<i64>,
}
#[derive(Default, Deserialize)]
struct Counts {
    total: usize,
    starred: usize,
    categories: HashMap<String, usize>,
    feeds: HashMap<String, usize>,
}
#[derive(Deserialize)]
struct Page {
    items: Vec<Article>,
    next: String,
}
#[derive(Clone)]
struct Source {
    title: String,
    category: Option<i64>,
    feed: Option<i64>,
}
impl Source {
    fn unread(&self, counts: &Counts) -> usize {
        if let Some(id) = self.feed {
            *counts.feeds.get(&id.to_string()).unwrap_or(&0)
        } else if let Some(id) = self.category {
            *counts.categories.get(&id.to_string()).unwrap_or(&0)
        } else {
            counts.total
        }
    }
    fn mark_read_body(&self) -> serde_json::Value {
        // A feed takes precedence over its collection: never mark its siblings.
        if let Some(id) = self.feed {
            serde_json::json!({"feedId": id})
        } else if let Some(id) = self.category {
            serde_json::json!({"categoryId": id})
        } else {
            serde_json::json!({})
        }
    }
    fn scope_label(&self) -> String {
        if self.feed.is_some() {
            format!("feed: {}", clean(&self.title))
        } else if self.category.is_some() {
            format!("collection: {}", clean(&self.title))
        } else {
            "your entire library".into()
        }
    }
    fn contains(&self, article: &Article, feeds: &[Feed]) -> bool {
        if let Some(id) = self.feed {
            article.feed_id == id
        } else if let Some(id) = self.category {
            feeds
                .iter()
                .any(|f| f.id == article.feed_id && f.category_id == Some(id))
        } else {
            true
        }
    }
}
enum Msg {
    Browser(Result<()>),
    Library(Result<(Vec<Feed>, Vec<Category>)>),
    Refresh(bool, Result<refresh::Progress>),
    Fresh(u64, u64, Result<Page>),
    List(u64, u64, Result<Page>, bool),
    Article(u64, u64, Result<Article>),
    Fulltext(u64, Result<Article>),
    Image(i64, Result<image::DynamicImage>),
    Counts(u64, Result<Counts>),
    State(u64, Result<Article>),
    Mutation(Result<Article>, Option<Article>, bool),
    MarkAll(Result<serde_json::Value>, Source),
}
fn get<T: serde::de::DeserializeOwned>(base: &str, path: &str) -> Result<T> {
    ureq::get(format!("{base}/api/{path}"))
        .config()
        .timeout_global(Some(Duration::from_secs(20)))
        .build()
        .call()
        .map_err(|e| e.to_string())?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())
}
fn encode(s: &str) -> String {
    s.bytes()
        .map(|b| {
            if b.is_ascii_alphanumeric() || b"-_.~".contains(&b) {
                (b as char).to_string()
            } else {
                format!("%{b:02X}")
            }
        })
        .collect()
}
fn clean(s: &str) -> String {
    s.chars()
        .filter(|c| !c.is_control() || *c == '\n' || *c == '\t')
        .collect()
}
fn date(s: &str) -> &str {
    s.get(5..10).unwrap_or(s)
}
struct App {
    theme: theme::Theme,
    base: String,
    manager: Option<manage::Manager>,
    command_menu: Option<commands::Menu>,
    gallery: Option<gallery::Gallery>,
    refresh: refresh::State,
    text_sizing: bool,
    large_headlines: bool,
    reading_queue: Vec<Article>,
    tx: Sender<Msg>,
    rx: Receiver<Msg>,
    categories: Vec<Category>,
    feeds: Vec<Feed>,
    sources: Vec<Source>,
    source: usize,
    source_state: ListState,
    show_feeds: bool,
    list_title: String,
    items: Vec<Article>,
    selected: usize,
    list_state: ListState,
    article: Option<Article>,
    counts: Counts,
    status: usize,
    query: String,
    searching: bool,
    next: String,
    focus: usize,
    layout_mode: layout::Mode,
    source_return_focus: usize,
    help_scroll: u16,
    zen: bool,
    help: bool,
    images: bool,
    picker: Picker,
    picture: Option<StatefulProtocol>,
    image_status: String,
    scroll: u16,
    text: String,
    links: Vec<String>,
    link_picker: Option<links::Picker>,
    text_width: u16,
    positions: HashMap<i64, usize>,
    pending_position: Option<usize>,
    generation: u64,
    detail_generation: u64,
    detail_loading: bool,
    loading: bool,
    mutating: bool,
    state_version: u64,
    states: HashMap<i64, (u64, bool, bool)>,
    read_timer: Option<(i64, Instant)>,
    reader_visible: bool,
    keep_unread: Option<i64>,
    mark_all: Option<Source>,
    undo: Vec<Article>,
    message: String,
    message_time: Instant,
    last_sync: Instant,
}
impl App {
    fn new(base: String, picker: Picker) -> Result<Self> {
        let categories: Vec<Category> = get(&base, "categories")?;
        let feeds: Vec<Feed> = get(&base, "feeds")?;
        let counts = get(&base, "counts")?;
        let mut app = Self::from_library(base, picker, categories, feeds, counts);
        app.load(false);
        Ok(app)
    }
    fn from_library(
        base: String,
        picker: Picker,
        categories: Vec<Category>,
        feeds: Vec<Feed>,
        counts: Counts,
    ) -> Self {
        let sources = std::iter::once(Source {
            title: "All sources".into(),
            category: None,
            feed: None,
        })
        .chain(categories.iter().map(|c| Source {
            title: c.title.clone(),
            category: Some(c.id),
            feed: None,
        }))
        .collect::<Vec<_>>();
        let source = 0;
        let (tx, rx) = mpsc::channel();
        Self {
            theme: theme::Theme::from_env(),
            base,
            manager: None,
            command_menu: None,
            gallery: None,
            refresh: refresh::State::default(),
            text_sizing: picker
                .capabilities()
                .contains(&ratatui_image::picker::Capability::TextSizingProtocol)
                && std::env::var_os("TMUX").is_none(),
            large_headlines: !std::env::args().any(|s| s == "--small-headlines"),
            reading_queue: vec![],
            tx,
            rx,
            categories,
            feeds,
            sources,
            source,
            source_state: ListState::default(),
            show_feeds: false,
            list_title: "All sources".into(),
            items: vec![],
            selected: 0,
            list_state: ListState::default(),
            article: None,
            counts,
            status: 0,
            query: String::new(),
            searching: false,
            next: String::new(),
            focus: 1,
            layout_mode: layout::Mode::Wide,
            source_return_focus: 1,
            help_scroll: 0,
            zen: false,
            help: false,
            images: true,
            picker,
            picture: None,
            image_status: String::new(),
            scroll: 0,
            text: String::new(),
            links: vec![],
            link_picker: None,
            text_width: 0,
            positions: HashMap::new(),
            pending_position: None,
            generation: 0,
            detail_generation: 0,
            detail_loading: false,
            loading: false,
            mutating: false,
            state_version: 0,
            states: HashMap::new(),
            read_timer: None,
            reader_visible: false,
            keep_unread: None,
            mark_all: None,
            undo: vec![],
            message: "● UNREAD / ✓ READ · Reading marks automatically · Shift+M marks all read"
                .into(),
            message_time: Instant::now(),
            last_sync: Instant::now(),
        }
    }
    fn notify(&mut self, text: impl Into<String>) {
        self.message = text.into();
        self.message_time = Instant::now()
    }
    fn load(&mut self, more: bool) {
        if !more {
            self.reading_queue.clear();
        }
        self.link_picker = None;
        self.read_timer = None;
        self.list_title = self.sources[self.source].title.clone();
        self.generation += 1;
        let request_id = self.generation;
        self.loading = true;
        let path = self.list_path(more);
        let (tx, base) = (self.tx.clone(), self.base.clone());
        let version = self.state_version;
        thread::spawn(move || {
            let result = get(&base, &path);
            let _ = tx.send(Msg::List(request_id, version, result, more));
            let _ = tx.send(Msg::Counts(version, get(&base, "counts")));
        });
    }
    fn list_path(&self, more: bool) -> String {
        let s = &self.sources[self.source];
        let mut path = format!(
            "articles?limit=100&status={}&q={}",
            ["all", "unread", "starred"][self.status],
            encode(&self.query)
        );
        if let Some(id) = s.category {
            path += &format!("&category={id}")
        };
        if let Some(id) = s.feed {
            path += &format!("&feed={id}")
        };
        if more {
            path += &format!("&cursor={}", encode(&self.next))
        }
        path
    }
    fn open(&mut self) {
        self.gallery = None;
        self.links.clear();
        self.link_picker = None;
        self.read_timer = None;
        self.save_position();
        let Some(a) = self.items.get(self.selected).cloned() else {
            self.detail_loading = false;
            self.article = None;
            self.picture = None;
            self.text.clear();
            self.pending_position = None;
            self.detail_generation += 1;
            return;
        };
        if self.article.as_ref().is_none_or(|old| old.id != a.id) {
            self.keep_unread = None;
        }
        self.detail_generation += 1;
        let request_id = self.detail_generation;
        self.detail_loading = true;
        self.pending_position = self.positions.get(&a.id).copied();
        self.scroll = 0;
        self.picture = None;
        self.text.clear();
        self.text_width = 0;
        self.image_status = if a.image_url.is_empty() {
            "No image in this entry"
        } else {
            "Loading image…"
        }
        .into();
        self.article = Some(a.clone());
        let (tx, base) = (self.tx.clone(), self.base.clone());
        let version = self.state_version;
        thread::spawn(move || {
            let _ = tx.send(Msg::Article(
                request_id,
                version,
                get(&base, &format!("articles/{}", a.id)),
            ));
            if !a.image_url.is_empty() {
                let result = gallery::fetch_image(&base, a.id, 0);
                let _ = tx.send(Msg::Image(a.id, result));
            }
        });
    }
    fn move_selection(&mut self, d: isize) {
        if self.focus == 2 || (self.zen && self.focus != 0) {
            self.scroll = self.scroll.saturating_add_signed(d as i16);
            return;
        }
        if self.focus == 0 {
            let source = self
                .source
                .saturating_add_signed(d)
                .min(self.sources.len().saturating_sub(1));
            if source != self.source {
                self.select_source(source);
            }
        } else {
            self.reading_queue.clear();
            let n = self
                .selected
                .saturating_add_signed(d)
                .min(self.items.len().saturating_sub(1));
            if n != self.selected
                || self
                    .items
                    .get(n)
                    .is_some_and(|a| !matches_status(a, self.status))
            {
                let target = self.items.get(n).map(|a| a.id);
                // A finished story stays visible until the reader moves away.
                // Resolve by ID after pruning so J never skips its successor.
                self.items.retain(|a| matches_status(a, self.status));
                self.selected = target
                    .and_then(|id| self.items.iter().position(|a| a.id == id))
                    .unwrap_or(n.min(self.items.len().saturating_sub(1)));
                self.open();
                if self.items.is_empty() && !self.next.is_empty() && !self.loading {
                    self.load(true);
                }
            }
        }
    }
    fn select_source(&mut self, source: usize) {
        self.detail_loading = false;
        self.gallery = None;
        self.reading_queue.clear();
        self.links.clear();
        self.link_picker = None;
        self.save_position();
        self.source = source;
        self.pending_position = None;
        // Invalidate article requests from the previous collection immediately.
        self.detail_generation += 1;
        self.read_timer = None;
        self.keep_unread = None;
        self.items.clear();
        self.article = None;
        self.picture = None;
        self.text.clear();
        self.text_width = 0;
        self.scroll = 0;
        self.selected = 0;
        self.list_state = ListState::default();
        self.next.clear();
        self.load(false);
    }
    fn save_position(&mut self) {
        if let Some(article) = &self.article {
            let offset = self
                .pending_position
                .unwrap_or_else(|| layout::content_offset(&self.text, self.scroll));
            self.positions.insert(article.id, offset);
        }
    }
    fn sources_panel(&mut self) {
        if self.focus == 0 {
            self.focus = self.source_return_focus;
        } else {
            self.source_return_focus = self.focus;
            self.focus = 0;
        }
    }
    fn focus_pane(&mut self, pane: usize) {
        if pane == 0 && self.focus != 0 {
            self.source_return_focus = self.focus;
        }
        self.focus = pane;
        self.zen = false;
    }
    fn cycle_pane(&mut self, backwards: bool) {
        let pane = if self.zen || self.focus == 0 && self.layout_mode != layout::Mode::Wide {
            1
        } else if self.layout_mode == layout::Mode::Wide {
            (self.focus + if backwards { 2 } else { 1 }) % 3
        } else if self.focus == 1 {
            2
        } else {
            1
        };
        self.focus_pane(pane);
    }
    fn tick_read(&mut self) {
        let Some((id, since)) = self.read_timer else {
            return;
        };
        if self.manager.is_some()
            || self.help
            || self.searching
            || self.mark_all.is_some()
            || self.link_picker.is_some()
            || self.command_menu.is_some()
            || self.gallery.is_some()
            || (self.focus == 0 && !self.zen)
            || self.loading
            || !self.reader_visible
        {
            self.read_timer = Some((id, Instant::now()));
            return;
        }
        if !self.mutating && since.elapsed() >= READ_DELAY {
            self.read_timer = None;
            if self.article.as_ref().is_some_and(|a| a.id == id && !a.read)
                && self.keep_unread != Some(id)
            {
                self.mutate(Some(true), None, false);
            }
        }
    }
    fn reconcile_state(&self, a: &mut Article, version: u64) {
        if let Some(&(changed, read, starred)) = self.states.get(&a.id) {
            if changed > version {
                a.read = read;
                a.starred = starred;
            }
        }
    }
    fn apply_state(&mut self, a: &Article) {
        for item in &mut self.reading_queue {
            if item.id == a.id {
                item.read = a.read;
                item.starred = a.starred;
            }
        }
        for x in &mut self.items {
            if x.id == a.id {
                x.read = a.read;
                x.starred = a.starred;
            }
        }
        if let Some(current) = &mut self.article {
            if current.id == a.id {
                current.read = a.read;
                current.starred = a.starred;
                if a.read {
                    self.read_timer = None;
                }
            }
        }
        let current = self.article.as_ref().map(|a| a.id);
        self.items
            .retain(|a| Some(a.id) == current || matches_status(a, self.status));
        self.selected = current
            .and_then(|id| self.items.iter().position(|a| a.id == id))
            .unwrap_or(0);
    }
    fn confirm_mark_all(&mut self) {
        if self.mutating {
            self.notify("A reading-state change is still saving. Try again in a moment.");
            return;
        }
        let source = self.sources[self.source].clone();
        self.mark_all = Some(source);
    }
    fn mark_all_read(&mut self, source: Source) {
        self.mutating = true;
        self.state_version += 1;
        self.read_timer = None;
        self.notify(format!("Marking all read in {}…", source.scope_label()));
        let (tx, base, version) = (self.tx.clone(), self.base.clone(), self.state_version + 1);
        thread::spawn(move || {
            let result = manage::request(
                &base,
                "POST",
                "articles/mark-read",
                Some(source.mark_read_body()),
                None,
            );
            let _ = tx.send(Msg::MarkAll(result, source));
            let _ = tx.send(Msg::Counts(version, get(&base, "counts")));
        });
    }
    fn mutate(&mut self, read: Option<bool>, starred: Option<bool>, undo: bool) {
        if self.mutating {
            return;
        }
        let a = if undo {
            self.undo.last().cloned()
        } else {
            self.article.clone()
        };
        let Some(old) = a else { return };
        self.mutating = true;
        self.state_version += 1;
        if read.is_some() || undo {
            if self.article.as_ref().is_some_and(|a| a.id == old.id) {
                self.read_timer = None;
                self.keep_unread = Some(old.id);
            }
        }
        let change = if undo {
            serde_json::json!({"read":old.read,"starred":old.starred})
        } else if let Some(read) = read {
            serde_json::json!({"read":read})
        } else {
            serde_json::json!({"starred":starred.unwrap_or(old.starred)})
        };
        let (tx, base) = (self.tx.clone(), self.base.clone());
        let version = self.state_version + 1;
        thread::spawn(move || {
            let result = (|| {
                ureq::patch(format!("{base}/api/articles/{}", old.id))
                    .config()
                    .timeout_global(Some(Duration::from_secs(5)))
                    .build()
                    .send_json(change)
                    .map_err(|e| e.to_string())?
                    .body_mut()
                    .read_json()
                    .map_err(|e| e.to_string())
            })();
            let _ = tx.send(Msg::Mutation(result, Some(old), undo));
            let _ = tx.send(Msg::Counts(version, get(&base, "counts")));
        });
    }
    fn messages(&mut self) {
        if let Some(gallery) = &mut self.gallery {
            gallery.tick();
        }
        self.poll_refresh();
        if let Some(manager) = &mut self.manager {
            manager.tick();
        }

        while let Ok(msg) = self.rx.try_recv() {
            match msg {
                Msg::Refresh(started, result) => {
                    if started {
                        self.refresh.requesting = false;
                    } else {
                        self.refresh.polling = false;
                    }
                    match result {
                        Ok(progress) => {
                            if self.refresh.accept(progress) {
                                let p = &self.refresh.progress;
                                self.notify(format!(
                                    "Refresh complete · {} new stories · {} feeds failed",
                                    p.inserted, p.failed
                                ));
                                self.sync_articles();
                            }
                        }
                        Err(e) => {
                            self.refresh.error = true;
                            if started {
                                self.notify(format!("Could not refresh feeds: {e}"));
                            }
                        }
                    }
                }
                Msg::Fresh(generation, version, result) if generation == self.generation => {
                    self.loading = false;
                    match result {
                        Ok(mut page) => {
                            let old = self.article.as_ref().map(|a| a.id);
                            for item in &mut page.items {
                                self.reconcile_state(item, version);
                            }
                            page.items
                                .retain(|a| matches_status(a, self.status) || Some(a.id) == old);
                            let ids = page
                                .items
                                .iter()
                                .map(|a| a.id)
                                .collect::<std::collections::HashSet<_>>();
                            page.items.extend(
                                self.items
                                    .iter()
                                    .filter(|a| {
                                        !ids.contains(&a.id)
                                            && (matches_status(a, self.status) || Some(a.id) == old)
                                    })
                                    .cloned(),
                            );
                            self.next = page.next;
                            self.items = page.items;
                            self.selected = old
                                .and_then(|id| self.items.iter().position(|a| a.id == id))
                                .unwrap_or(0);
                            if self.article.is_none() && !self.items.is_empty() {
                                self.open();
                            }
                        }
                        Err(e) => self.notify(format!(
                            "Feeds checked, but headlines could not reload: {e}. Ctrl+R retries."
                        )),
                    }
                }
                Msg::Browser(result) => match result {
                    Ok(()) => self.notify("Sent link to your browser"),
                    Err(error) => self.notify(format!("Could not open link: {error}")),
                },
                Msg::List(request_id, version, result, more) if request_id == self.generation => {
                    self.loading = false;
                    match result {
                        Ok(mut p) => {
                            for a in &mut p.items {
                                self.reconcile_state(a, version);
                            }
                            p.items.retain(|a| matches_status(a, self.status));
                            let old = self.article.as_ref().map(|a| a.id);
                            if more {
                                for item in p.items {
                                    if !self.reading_queue.is_empty()
                                        && !self.reading_queue.iter().any(|a| a.id == item.id)
                                    {
                                        self.reading_queue.push(item.clone());
                                    }
                                    if !self.items.iter().any(|a| a.id == item.id) {
                                        self.items.push(item);
                                    }
                                }
                            } else {
                                self.items = p.items
                            };
                            self.next = p.next;
                            self.selected = old
                                .and_then(|id| self.items.iter().position(|a| a.id == id))
                                .unwrap_or(0);
                            if self.items.is_empty() {
                                self.links.clear();
                                self.link_picker = None;
                                self.article = None;
                                self.picture = None;
                                self.text.clear();
                                self.detail_generation += 1
                            } else {
                                self.open()
                            }
                        }
                        Err(e) => self.notify(format!("Could not load articles: {e}")),
                    }
                }
                Msg::Article(request_id, version, result)
                    if request_id == self.detail_generation =>
                {
                    self.detail_loading = false;
                    match result {
                        Ok(mut a) => {
                            self.reconcile_state(&mut a, version);
                            self.apply_state(&a);
                            if !a.read && self.keep_unread != Some(a.id) {
                                self.read_timer = Some((a.id, Instant::now()));
                            }
                            self.article = Some(a);
                            self.text_width = 0
                        }
                        Err(e) => self.notify(e),
                    }
                }
                Msg::Image(id, result) if self.article.as_ref().is_some_and(|a| a.id == id) => {
                    match result {
                        Ok(img) => {
                            self.picture = Some(self.picker.new_resize_protocol(img));
                            self.image_status.clear();
                        }
                        Err(_) => self.image_status = "Image unavailable · O opens original".into(),
                    }
                }
                Msg::Fulltext(version, result) => match result {
                    Ok(mut a) => {
                        self.reconcile_state(&mut a, version);
                        if let Some(old) = self.article.as_ref().filter(|old| old.id == a.id) {
                            // Full-text extraction may finish after a bulk read or
                            // a state poll; it must never overwrite reading state.
                            a.read = old.read;
                            a.starred = old.starred;
                            self.article = Some(a);
                            self.text_width = 0;
                            self.notify("Full article text loaded.")
                        }
                    }
                    Err(e) => self.notify(e),
                },
                Msg::Library(Ok((feeds, categories))) => {
                    let previous = self.sources[self.source].clone();
                    self.feeds = feeds;
                    self.categories = categories;
                    self.rebuild_sources();
                    let selected = self
                        .sources
                        .iter()
                        .position(|s| s.feed == previous.feed && s.category == previous.category)
                        .unwrap_or(0);
                    self.source = selected;
                    self.list_title = self.sources[selected].title.clone();
                    if self.sources[selected].feed != previous.feed
                        || self.sources[selected].category != previous.category
                    {
                        self.select_source(selected);
                    }
                }
                Msg::Counts(version, Ok(c)) if version == self.state_version && !self.mutating => {
                    self.counts = c;
                }
                Msg::State(version, Ok(a)) if version == self.state_version && !self.mutating => {
                    self.apply_state(&a);
                }
                Msg::Mutation(result, old, undo) => {
                    self.mutating = false;
                    self.state_version += 1;
                    match result {
                        Ok(a) => {
                            let changed_read = old.as_ref().is_some_and(|old| old.read != a.read);
                            if undo {
                                self.undo.pop();
                            } else if let Some(old) = old {
                                self.undo.push(old);
                            }
                            self.states
                                .insert(a.id, (self.state_version, a.read, a.starred));
                            self.apply_state(&a);
                            self.notify(if undo {
                                "Last action undone"
                            } else if changed_read {
                                if a.read {
                                    "Marked read · U undo"
                                } else {
                                    "Marked unread · U undo"
                                }
                            } else if a.starred {
                                "Saved for later · U undo"
                            } else {
                                "Removed from saved · U undo"
                            });
                        }
                        Err(e) => self.notify(format!("Change failed: {e}. Try again.")),
                    }
                }
                Msg::MarkAll(result, source) => {
                    self.mutating = false;
                    self.state_version += 1;
                    match result {
                        Ok(value) => {
                            let marked = value["marked"].as_u64().unwrap_or(0);
                            let affected = self
                                .items
                                .iter()
                                .filter(|a| source.contains(a, &self.feeds))
                                .cloned()
                                .collect::<Vec<_>>();
                            for mut a in affected {
                                a.read = true;
                                self.states
                                    .insert(a.id, (self.state_version, a.read, a.starred));
                                self.apply_state(&a);
                            }
                            self.undo.clear();
                            self.read_timer = None;
                            self.detail_generation += 1;
                            // Freshly reload the current scope, including results beyond
                            // the loaded page. This also clears a finished Unread view.
                            self.load(false);
                            self.notify(format!(
                                "Marked {marked} articles read in {}",
                                source.scope_label()
                            ));
                        }
                        Err(e) => self.notify(format!("Could not mark all read: {e}")),
                    }
                }
                _ => {}
            }
        }
    }
    fn browse_links(&mut self) {
        // A thin window may not have rendered the selected article yet.
        self.ensure_text(if self.text_width == 0 {
            65
        } else {
            self.text_width
        });
        let Some(article) = &self.article else {
            self.notify("Select an article first");
            return;
        };
        if self.links.is_empty() {
            self.notify("This article has no numbered links · O opens the original");
            return;
        }
        self.link_picker = Some(links::Picker::new(
            article.title.clone(),
            article.url.clone(),
            self.links.clone(),
        ));
    }
    fn open_browser(&mut self, url: &str) {
        let url = match links::web_url(url, "") {
            Ok(url) => url,
            Err(error) => {
                self.notify(error);
                return;
            }
        };
        self.notify("Opening link in your browser…");
        let tx = self.tx.clone();
        thread::spawn(move || {
            // Pass the URL as one argument, never as a shell command. Waiting
            // here also reaps the opener without blocking the reader.
            let result = std::process::Command::new(if cfg!(target_os = "macos") {
                "open"
            } else {
                "xdg-open"
            })
            .arg(url)
            .stdin(std::process::Stdio::null())
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .status()
            .map_err(|e| e.to_string())
            .and_then(|status| {
                if status.success() {
                    Ok(())
                } else {
                    Err(format!("browser opener exited with {status}"))
                }
            });
            let _ = tx.send(Msg::Browser(result));
        });
    }
    fn toggle_sources(&mut self) {
        self.show_feeds = !self.show_feeds;
        self.rebuild_sources();
        self.select_source(0);
        self.focus_pane(0);
        self.notify("J/K updates articles · Enter moves into the article list")
    }
    fn rebuild_sources(&mut self) {
        self.sources = vec![Source {
            title: "All sources".into(),
            category: None,
            feed: None,
        }];
        if self.show_feeds {
            self.sources.extend(self.feeds.iter().map(|f| Source {
                title: f.title.clone(),
                category: f.category_id,
                feed: Some(f.id),
            }))
        } else {
            self.sources.extend(self.categories.iter().map(|c| Source {
                title: c.title.clone(),
                category: Some(c.id),
                feed: None,
            }))
        };
    }
    fn event(&mut self, event: Event) -> bool {
        if let Event::Resize(width, _) = event {
            self.layout_mode = layout::Mode::for_width(width);
            self.text_width = 0;
            return false;
        }
        let Event::Key(key) = event else { return false };
        if key.kind == KeyEventKind::Release {
            return false;
        }
        if key.modifiers.contains(KeyModifiers::CONTROL) && key.code == KeyCode::Char('c') {
            return true;
        }
        if let Some(menu) = &mut self.command_menu {
            match menu.key(key) {
                commands::Action::Stay => {}
                commands::Action::Close => self.command_menu = None,
                commands::Action::Run(command) => {
                    self.command_menu = None;
                    return self.event(Event::Key(command));
                }
            }
            return false;
        }
        if let Some(gallery) = &mut self.gallery {
            match gallery.key(key.code) {
                gallery::Action::Stay => {}
                gallery::Action::Close => self.gallery = None,
                gallery::Action::Open(url) => self.open_browser(&url),
            }
            return false;
        }
        if let Some(manager) = &mut self.manager {
            if manager.event(key) {
                let changed = manager.changed;
                self.manager = None;
                let (tx, base) = (self.tx.clone(), self.base.clone());
                thread::spawn(move || {
                    let _ = tx.send(Msg::Library((|| {
                        Ok((get(&base, "feeds")?, get(&base, "categories")?))
                    })()));
                });
                if changed {
                    self.load(false)
                }
            }
            return false;
        }
        if let Some(source) = self.mark_all.clone() {
            match key.code {
                KeyCode::Char('y' | 'Y') | KeyCode::Enter => {
                    self.mark_all = None;
                    self.mark_all_read(source);
                }
                KeyCode::Char('n' | 'N' | 'q') | KeyCode::Esc => {
                    self.mark_all = None;
                    self.notify("Mark all read cancelled");
                }
                _ => {}
            }
            return false;
        }
        if let Some(picker) = &mut self.link_picker {
            match picker.key(key.code) {
                links::Action::Stay => {}
                links::Action::Close => self.link_picker = None,
                links::Action::Open(url) => {
                    self.link_picker = None;
                    self.open_browser(&url);
                }
            }
            return false;
        }
        if self.searching {
            match key.code {
                KeyCode::Esc => {
                    self.searching = false;
                    self.query.clear()
                }
                KeyCode::Enter => {
                    self.searching = false;
                    self.load(false)
                }
                KeyCode::Backspace => {
                    self.query.pop();
                }
                KeyCode::Char(c) => self.query.push(c),
                _ => {}
            }
            return false;
        }
        if self.help {
            match key.code {
                KeyCode::Esc | KeyCode::Char('?') | KeyCode::Char('q') => self.help = false,
                KeyCode::Down | KeyCode::Char('j') => {
                    self.help_scroll = self.help_scroll.saturating_add(1)
                }
                KeyCode::Up | KeyCode::Char('k') => {
                    self.help_scroll = self.help_scroll.saturating_sub(1)
                }
                KeyCode::PageDown => self.help_scroll = self.help_scroll.saturating_add(10),
                KeyCode::PageUp => self.help_scroll = self.help_scroll.saturating_sub(10),
                KeyCode::Home => self.help_scroll = 0,
                KeyCode::End => self.help_scroll = u16::MAX,
                _ => {}
            }
            return false;
        }
        match key.code {
            KeyCode::Char('p') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                self.show_commands()
            }
            KeyCode::Char('r') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                self.sync_articles()
            }
            KeyCode::Char('[') => self.navigate_article(-1),
            KeyCode::Char(']') => self.navigate_article(1),
            KeyCode::Char('H') => {
                self.large_headlines = !self.large_headlines;
                self.notify(if !self.text_sizing {
                    "This terminal does not report text sizing support"
                } else if self.large_headlines {
                    "Large reader headlines enabled"
                } else {
                    "Normal reader headlines enabled"
                });
            }
            KeyCode::Char('v') => {
                if let Some(a) = &self.article {
                    self.gallery = Some(gallery::Gallery::new(
                        self.base.clone(),
                        a.id,
                        clean(&a.title),
                    ));
                }
            }
            KeyCode::Char('b') => self.browse_links(),
            KeyCode::Char('t') => {
                if let Some(a) = &self.article {
                    let (tx, base, id, version) =
                        (self.tx.clone(), self.base.clone(), a.id, self.state_version);
                    self.notify("Loading full article text…");
                    thread::spawn(move || {
                        let result = manage::request(
                            &base,
                            "POST",
                            &format!("articles/{id}/fulltext"),
                            None,
                            None,
                        )
                        .and_then(|v| serde_json::from_value(v).map_err(|e| e.to_string()));
                        let _ = tx.send(Msg::Fulltext(version, result));
                    });
                }
            }
            KeyCode::Char('g') => {
                self.manager = Some(manage::Manager::new(self.base.clone(), false))
            }
            KeyCode::Char('a') => {
                self.manager = Some(manage::Manager::new(self.base.clone(), true))
            }
            KeyCode::Char('R') => self.start_refresh(),
            KeyCode::Char('q') => return true,
            KeyCode::Char('?') => {
                self.help = true;
                self.help_scroll = 0;
            }
            KeyCode::Char('c') => self.sources_panel(),
            KeyCode::Char('j') | KeyCode::Down => self.move_selection(1),
            KeyCode::Char('k') | KeyCode::Up => self.move_selection(-1),
            KeyCode::Tab => self.cycle_pane(false),
            KeyCode::BackTab => self.cycle_pane(true),
            KeyCode::Char('h') | KeyCode::Left => self.focus_pane(self.focus.saturating_sub(1)),
            KeyCode::Char('l') | KeyCode::Right => self.focus_pane((self.focus + 1).min(2)),
            KeyCode::Enter => {
                if self.focus == 0 {
                    self.focus_pane(1)
                } else {
                    self.focus_pane(2)
                }
            }
            KeyCode::Char('1') => {
                self.status = 0;
                self.load(false)
            }
            KeyCode::Char('2') => {
                self.status = 1;
                self.load(false)
            }
            KeyCode::Char('3') => {
                self.status = 2;
                self.load(false)
            }
            KeyCode::Char('/') => self.searching = true,
            KeyCode::Char('s') => {
                if let Some(a) = &self.article {
                    self.mutate(None, Some(!a.starred), false)
                }
            }
            KeyCode::Char('M') => self.confirm_mark_all(),
            KeyCode::Char('m') => {
                if let Some(a) = &self.article {
                    self.mutate(Some(!a.read), None, false)
                }
            }
            KeyCode::Char('u') => self.mutate(None, None, true),
            KeyCode::Char('z') => {
                self.zen = !self.zen;
                self.focus = if self.zen { 2 } else { 1 };
            }
            KeyCode::Char('i') => self.images = !self.images,
            KeyCode::Char('f') => self.toggle_sources(),
            KeyCode::Char('r') => self.start_refresh(),
            KeyCode::Char('n') => {
                if !self.next.is_empty() && !self.loading {
                    self.load(true)
                } else {
                    self.notify("All articles in this view are loaded")
                }
            }
            KeyCode::PageDown => self.scroll = self.scroll.saturating_add(12),
            KeyCode::PageUp => self.scroll = self.scroll.saturating_sub(12),
            KeyCode::Home => self.scroll = 0,
            KeyCode::Char('o') => {
                if let Some(url) = self.article.as_ref().map(|a| a.url.clone()) {
                    self.open_browser(&url);
                }
            }
            KeyCode::Esc => {
                if self.focus == 0 {
                    self.focus = self.source_return_focus;
                } else if self.focus == 2 || self.zen {
                    self.focus_pane(1);
                } else if !self.query.is_empty() {
                    self.query.clear();
                    self.load(false)
                }
            }
            _ => {}
        }
        false
    }
}
fn matches_status(a: &Article, status: usize) -> bool {
    match status {
        1 => !a.read,
        2 => a.starred,
        _ => true,
    }
}
fn panel(title: &str, active: bool, colors: Palette) -> Block<'_> {
    Block::default()
        .title(title)
        .borders(Borders::ALL)
        .border_style(Style::default().fg(if active { colors.accent } else { colors.line }))
        .style(Style::default().bg(colors.panel))
}
fn wrap_text(text: &str, width: usize) -> String {
    layout::wrap(text, width)
}
fn main() -> std::result::Result<(), Box<dyn std::error::Error>> {
    if std::env::args().any(|s| s == "--theme-check") {
        let theme = theme::Theme::from_env();
        println!(
            "Theme: {}",
            theme
                .source()
                .map(|p| p.display().to_string())
                .unwrap_or_else(|| "Current classic".into())
        );
        println!("{:#?}", theme.colors);
        return Ok(());
    }
    let base = std::env::var("CURRENT_URL").unwrap_or_else(|_| "http://127.0.0.1:8490".into());
    if std::env::args().any(|s| s == "--check") {
        let c: Counts = get(&base, "counts").map_err(io::Error::other)?;
        let p: Page = get(&base, "articles?limit=1").map_err(io::Error::other)?;
        println!(
            "Connected: {} unread, {} saved. Sample: {}",
            c.total,
            c.starred,
            p.items.first().map(|a| a.title.as_str()).unwrap_or("empty")
        );
        return Ok(());
    }
    let picker = if std::env::args().any(|s| s == "--text-images") {
        Picker::halfblocks()
    } else {
        Picker::from_query_stdio_with_options(
            ratatui_image::picker::cap_parser::QueryStdioOptions {
                text_sizing_protocol: true,
                ..Default::default()
            },
        )
        .unwrap_or_else(|_| Picker::halfblocks())
    };
    let mut app = App::new(base, picker).map_err(io::Error::other)?;
    let mut terminal = ratatui::init();
    let mut window_progress = refresh::WindowProgress::new();
    let result = (|| -> io::Result<()> {
        loop {
            app.messages();
            terminal.draw(|f| app.draw(f))?;
            window_progress.update(&app.refresh)?;
            app.tick_read();
            if event::poll(Duration::from_millis(80))? && app.event(event::read()?) {
                break;
            }
            if app.last_sync.elapsed() > Duration::from_secs(4) {
                app.last_sync = Instant::now();
                let (tx, base, id, version) = (
                    app.tx.clone(),
                    app.base.clone(),
                    app.article.as_ref().map(|a| a.id),
                    app.state_version,
                );
                thread::spawn(move || {
                    let _ = tx.send(Msg::Counts(version, get(&base, "counts")));
                    let _ = tx.send(Msg::Library((|| {
                        Ok((get(&base, "feeds")?, get(&base, "categories")?))
                    })()));
                    if let Some(id) = id {
                        let _ = tx.send(Msg::State(version, get(&base, &format!("articles/{id}"))));
                    }
                });
            }
        }
        Ok(())
    })();
    drop(window_progress);
    ratatui::restore();
    result?;
    Ok(())
}
#[cfg(test)]
mod tests {
    use super::*;
    use crossterm::event::KeyEvent;

    fn wait_for_articles(app: &mut App) {
        let deadline = Instant::now() + Duration::from_secs(5);
        loop {
            app.messages();
            if !app.loading && (app.items.is_empty() || app.article.is_some()) {
                return;
            }
            assert!(Instant::now() < deadline, "article navigation timed out");
            thread::sleep(Duration::from_millis(10));
        }
    }

    #[test]
    #[ignore = "requires the local Current server; performs read-only requests"]
    fn navigation_updates_articles_without_enter() {
        let base =
            std::env::var("CURRENT_TEST_URL").unwrap_or_else(|_| "http://127.0.0.1:8490".into());
        let mut app = App::new(base.clone(), Picker::halfblocks()).unwrap();
        wait_for_articles(&mut app);
        let initial_source = app.source;
        let initial_article = app.items[0].clone();
        let old_list_generation = app.generation;
        let old_detail_generation = app.detail_generation;

        app.focus = 0;
        app.event(Event::Key(KeyEvent::new(KeyCode::Down, KeyModifiers::NONE)));
        assert_eq!(app.source, initial_source + 1);
        assert!(
            app.article.is_none(),
            "old article must clear during source change"
        );
        assert!(
            app.items.is_empty(),
            "old headlines must clear during source change"
        );
        wait_for_articles(&mut app);
        let category = app.sources[app.source].category.unwrap();
        let expected: Page =
            get(&base, &format!("articles?category={category}&limit=100")).unwrap();
        assert!(!expected.items.is_empty());
        assert_eq!(app.items[0].id, expected.items[0].id);
        assert_eq!(app.focus, 0, "browsing must keep focus in source pane");

        // A slow reply from the previous source must not replace the new view.
        app.tx
            .send(Msg::List(
                old_list_generation,
                0,
                Ok(Page {
                    items: vec![initial_article.clone()],
                    next: String::new(),
                }),
                false,
            ))
            .unwrap();
        app.tx
            .send(Msg::Article(
                old_detail_generation,
                0,
                Ok(initial_article.clone()),
            ))
            .unwrap();
        app.messages();
        assert_eq!(app.items[0].id, expected.items[0].id);
        assert_eq!(app.article.as_ref().unwrap().id, expected.items[0].id);

        let generation = app.generation;
        app.event(Event::Key(KeyEvent::new(
            KeyCode::Enter,
            KeyModifiers::NONE,
        )));
        assert_eq!(app.focus, 1);
        assert_eq!(
            app.generation, generation,
            "Enter should focus, not refetch"
        );

        // Individual feeds follow the same live-selection behavior.
        app.toggle_sources();
        wait_for_articles(&mut app);
        let target = app
            .sources
            .iter()
            .position(|s| s.feed == Some(initial_article.feed_id))
            .unwrap();
        app.source = target - 1;
        app.event(Event::Key(KeyEvent::new(
            KeyCode::Char('j'),
            KeyModifiers::NONE,
        )));
        wait_for_articles(&mut app);
        assert!(!app.items.is_empty());
        assert!(
            app.items
                .iter()
                .all(|a| a.feed_id == initial_article.feed_id)
        );
    }

    #[test]
    fn query_encoding() {
        assert_eq!(encode("radio & signals"), "radio%20%26%20signals")
    }
    #[test]
    fn terminal_controls_removed() {
        assert_eq!(clean("hello\x1b[31m\x07 world\n"), "hello[31m world\n")
    }
    #[test]
    fn wrap_without_losing_words() {
        assert_eq!(
            wrap_text("a short news headline", 10),
            "a short\nnews\nheadline"
        )
    }
}
