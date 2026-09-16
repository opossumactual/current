use super::*;
use crossterm::event::KeyEvent;

impl App {
    pub(super) fn show_commands(&mut self) {
        use commands::Command as C;
        let story = self.article.is_some();
        let mut reload = C::new(
            "Reload cached stories",
            "Reload shared reading state without fetching publishers.",
            "Ctrl+R",
            KeyCode::Char('r'),
        );
        reload.key = KeyEvent::new(KeyCode::Char('r'), KeyModifiers::CONTROL);
        self.command_menu = Some(commands::Menu::new(vec![
            C::new(
                "Refresh feeds",
                "Fetch updates for active feeds, then update headlines.",
                "r",
                KeyCode::Char('r'),
            )
            .needs(
                !self.refresh.busy(),
                "A refresh is already running. Its progress is in the header.",
            ),
            C::new(
                "View article images",
                "Open the image gallery; zoom, pan and browse captions.",
                "v",
                KeyCode::Char('v'),
            )
            .needs(story, "Select an article first."),
            C::new(
                "Toggle large headlines",
                "Larger reader titles in terminals with text sizing support.",
                "Shift+H",
                KeyCode::Char('H'),
            )
            .needs(
                self.text_sizing,
                "This terminal does not report text sizing support.",
            ),
            C::new(
                "Next article",
                "Read the next story in this feed, search or filter.",
                "]",
                KeyCode::Char(']'),
            )
            .needs(story, "Select an article first."),
            C::new(
                "Previous article",
                "Go back and restore the previous reading position.",
                "[",
                KeyCode::Char('['),
            )
            .needs(story, "Select an article first."),
            C::new(
                "Open article links",
                "Browse numbered references and open one in the browser.",
                "b",
                KeyCode::Char('b'),
            )
            .needs(story, "Select an article first."),
            C::new(
                "Load full article text",
                "Retrieve the full story from its publisher.",
                "t",
                KeyCode::Char('t'),
            )
            .needs(story, "Select an article first."),
            C::new(
                "Open original in browser",
                "Open the selected story's website.",
                "o",
                KeyCode::Char('o'),
            )
            .needs(story, "Select an article first."),
            C::new(
                "Save / unsave article",
                "Bookmark this story for later.",
                "s",
                KeyCode::Char('s'),
            )
            .needs(story, "Select an article first."),
            C::new(
                "Toggle read / unread",
                "Change the current story's reading state.",
                "m",
                KeyCode::Char('m'),
            )
            .needs(story, "Select an article first."),
            C::new(
                "Mark all read",
                "Confirm marking every article in the selected source read.",
                "Shift+M",
                KeyCode::Char('M'),
            ),
            C::new(
                "Undo last reading action",
                "Restore the last read or saved state.",
                "u",
                KeyCode::Char('u'),
            )
            .needs(!self.undo.is_empty(), "There is no reading action to undo."),
            C::new(
                "Show sources",
                "Choose a collection or individual feed.",
                "c",
                KeyCode::Char('c'),
            ),
            C::new(
                "Switch collections / feeds",
                "Change the source picker between collections and feeds.",
                "f",
                KeyCode::Char('f'),
            ),
            C::new(
                "Manage feeds",
                "Edit subscriptions, schedules and collections; import or export OPML.",
                "g",
                KeyCode::Char('g'),
            ),
            C::new(
                "Add a feed",
                "Subscribe using a website or RSS address.",
                "a",
                KeyCode::Char('a'),
            ),
            C::new(
                "All stories",
                "Show read and unread articles.",
                "1",
                KeyCode::Char('1'),
            ),
            C::new(
                "Unread stories",
                "Show unread articles in this source.",
                "2",
                KeyCode::Char('2'),
            ),
            C::new(
                "Saved stories",
                "Show bookmarked articles.",
                "3",
                KeyCode::Char('3'),
            ),
            C::new(
                "Search articles",
                "Search titles and feed content.",
                "/",
                KeyCode::Char('/'),
            ),
            C::new(
                "Toggle focused reader",
                "Expand the reading room to the full window.",
                "z",
                KeyCode::Char('z'),
            ),
            C::new(
                "Toggle image preview",
                "Show or hide the lead image in the article.",
                "i",
                KeyCode::Char('i'),
            ),
            C::new(
                "Load more headlines",
                "Load the next page of results.",
                "n",
                KeyCode::Char('n'),
            ),
            reload,
            C::new(
                "Keyboard help",
                "Show all keyboard shortcuts.",
                "?",
                KeyCode::Char('?'),
            ),
            C::new(
                "Quit Current",
                "Close this reader; feed polling continues in the server.",
                "q",
                KeyCode::Char('q'),
            ),
        ]));
    }
    pub(super) fn start_refresh(&mut self) {
        if self.refresh.busy() && !self.refresh.error {
            self.notify("Refresh already running");
            return;
        }
        self.refresh.requesting = true;
        self.refresh.error = false;
        let (tx, base) = (self.tx.clone(), self.base.clone());
        thread::spawn(move || {
            let result = manage::request(&base, "POST", "refresh", None, None)
                .and_then(|v| serde_json::from_value(v).map_err(|e| e.to_string()));
            let _ = tx.send(Msg::Refresh(true, result));
        });
    }
    pub(super) fn poll_refresh(&mut self) {
        let interval = if self.refresh.busy() {
            Duration::from_millis(750)
        } else {
            Duration::from_secs(4)
        };
        if self.refresh.polling || self.refresh.last_poll.elapsed() < interval {
            return;
        }
        self.refresh.polling = true;
        self.refresh.last_poll = Instant::now();
        let (tx, base) = (self.tx.clone(), self.base.clone());
        thread::spawn(move || {
            let _ = tx.send(Msg::Refresh(false, get(&base, "refresh")));
        });
    }
    pub(super) fn sync_articles(&mut self) {
        self.generation += 1;
        let (tx, base, path, generation, version) = (
            self.tx.clone(),
            self.base.clone(),
            self.list_path(false),
            self.generation,
            self.state_version,
        );
        thread::spawn(move || {
            let _ = tx.send(Msg::Fresh(generation, version, get(&base, &path)));
            let _ = tx.send(Msg::Counts(version, get(&base, "counts")));
        });
    }
    pub(super) fn navigate_article(&mut self, d: isize) {
        let Some(id) = self.article.as_ref().map(|a| a.id) else {
            return;
        };
        if self.reading_queue.is_empty() {
            self.reading_queue = self.items.clone();
        }
        let Some(index) = self.reading_queue.iter().position(|a| a.id == id) else {
            self.reading_queue.clear();
            return;
        };
        let Some(target) = index
            .checked_add_signed(d)
            .and_then(|i| self.reading_queue.get(i))
            .cloned()
        else {
            self.notify(if d < 0 {
                "First article in this view"
            } else if self.next.is_empty() {
                "Last article in this view"
            } else {
                "End of loaded stories · N loads more"
            });
            return;
        };
        self.selected = if let Some(i) = self.items.iter().position(|a| a.id == target.id) {
            i
        } else {
            self.items.push(target);
            self.items.len() - 1
        };
        self.focus = 2;
        self.open();
    }
}
