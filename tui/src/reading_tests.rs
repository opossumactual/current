use super::*;
use crossterm::event::KeyEvent;
use ratatui::{Terminal, backend::TestBackend};
use serde_json::json;

fn key(app: &mut App, code: KeyCode) {
    app.event(Event::Key(KeyEvent::new(code, KeyModifiers::NONE)));
}
fn sample(id: i64, read: bool) -> Article {
    Article {
        id,
        feed_id: 10,
        title: format!("Story {id}"),
        feed_title: "Field Notes".into(),
        content: "<p>A story to read.</p>".into(),
        read,
        ..Article::default()
    }
}
fn app() -> App {
    let mut app = App::from_library(
        "http://127.0.0.1:0".into(),
        Picker::halfblocks(),
        vec![],
        vec![],
        Counts::default(),
    );
    app.items = vec![sample(1, false), sample(2, false), sample(3, false)];
    app.article = Some(app.items[0].clone());
    app.reader_visible = true;
    app
}
fn screen(app: &mut App, width: u16, height: u16) -> String {
    let mut terminal = Terminal::new(TestBackend::new(width, height)).unwrap();
    terminal.draw(|f| app.draw(f)).unwrap();
    terminal
        .backend()
        .buffer()
        .content
        .iter()
        .map(|c| c.symbol())
        .collect()
}

#[test]
fn command_menu_searches_and_preserves_confirmation() {
    let mut app = app();
    app.event(Event::Key(KeyEvent::new(
        KeyCode::Char('p'),
        KeyModifiers::CONTROL,
    )));
    for c in "mark all".chars() {
        key(&mut app, KeyCode::Char(c));
    }
    let view = screen(&mut app, 65, 18);
    assert!(view.contains("Mark all read"));
    app.read_timer = Some((1, Instant::now() - READ_DELAY));
    app.tick_read();
    assert!(!app.mutating);
    key(&mut app, KeyCode::Enter);
    assert!(app.command_menu.is_none());
    assert!(app.mark_all.is_some());
    assert!(
        !app.mutating,
        "commands must use the existing bulk confirmation"
    );
    key(&mut app, KeyCode::Esc);
    app.show_commands();
    for c in "large".chars() {
        key(&mut app, KeyCode::Char(c));
    }
    key(&mut app, KeyCode::Enter);
    assert!(
        app.command_menu.is_some(),
        "unsupported commands remain disabled"
    );
    assert!(screen(&mut app, 80, 25).contains("does not report text sizing support"));
    key(&mut app, KeyCode::Esc);
    assert_eq!(app.focus, 1);
}

#[test]
fn brackets_revisit_read_stories_in_an_unread_queue() {
    let mut app = app();
    app.status = 1;
    app.focus = 2;
    app.items[0].content = "<p>A paragraph worth coming back to.</p>".repeat(100);
    app.article = Some(app.items[0].clone());
    screen(&mut app, 80, 25);
    app.scroll = 12;
    let offset = layout::content_offset(&app.text, app.scroll);
    app.apply_state(&sample(1, true));
    key(&mut app, KeyCode::Char(']'));
    assert_eq!(app.article.as_ref().unwrap().id, 2);
    assert_eq!(app.focus, 2);
    app.apply_state(&sample(2, true));
    assert!(!app.items.iter().any(|a| a.id == 1));
    key(&mut app, KeyCode::Char('['));
    assert_eq!(app.article.as_ref().unwrap().id, 1);
    assert!(app.article.as_ref().unwrap().read);
    screen(&mut app, 80, 25);
    assert_eq!(layout::content_offset(&app.text, app.scroll), offset);
    key(&mut app, KeyCode::Char(']'));
    key(&mut app, KeyCode::Char(']'));
    assert_eq!(app.article.as_ref().unwrap().id, 3);
}

#[test]
fn bracket_return_waits_for_full_content_before_restoring_position() {
    let mut app = app();
    app.focus = 2;
    let mut full = sample(1, false);
    full.content = "<p>A paragraph in a longer article.</p>".repeat(100);
    app.items[0].content.clear();
    app.items[0].summary = "Only a short preview.".into();
    app.article = Some(full.clone());
    screen(&mut app, 80, 25);
    app.scroll = 12;
    let offset = layout::content_offset(&app.text, app.scroll);
    key(&mut app, KeyCode::Char(']'));
    key(&mut app, KeyCode::Char('['));
    screen(&mut app, 80, 25);
    assert_eq!(
        app.pending_position,
        Some(offset),
        "the summary must not consume the saved position"
    );
    app.tx
        .send(Msg::Article(
            app.detail_generation,
            app.state_version,
            Ok(full),
        ))
        .unwrap();
    app.messages();
    screen(&mut app, 80, 25);
    assert_eq!(layout::content_offset(&app.text, app.scroll), offset);
    assert!(app.pending_position.is_none());
}

#[test]
fn refreshing_keeps_the_current_article_and_ignores_an_old_scope() {
    let mut app = app();
    app.focus = 2;
    app.status = 1;
    app.article.as_mut().unwrap().content = "<p>Keep my place in this story.</p>".repeat(100);
    screen(&mut app, 80, 25);
    app.scroll = 12;
    let text = app.text.clone();
    let detail = app.detail_generation;
    app.apply_state(&sample(1, true));
    app.tx
        .send(Msg::Fresh(
            app.generation,
            app.state_version,
            Ok(Page {
                items: vec![sample(9, false), sample(2, false)],
                next: "next-page".into(),
            }),
        ))
        .unwrap();
    app.messages();
    assert_eq!(app.article.as_ref().unwrap().id, 1);
    assert_eq!(app.items[app.selected].id, 1);
    assert_eq!(app.items[0].id, 9);
    assert_eq!(app.text, text);
    assert_eq!(app.scroll, 12);
    assert_eq!(app.detail_generation, detail);
    app.generation += 1;
    app.tx
        .send(Msg::Fresh(
            app.generation - 1,
            0,
            Ok(Page {
                items: vec![sample(99, false)],
                next: String::new(),
            }),
        ))
        .unwrap();
    app.messages();
    assert_eq!(app.items[0].id, 9);
}

#[test]
fn large_headlines_restore_cleanly_after_a_command_overlay() {
    use ratatui::backend::{Backend, CrosstermBackend};
    let mut app = app();
    app.text_sizing = true;
    app.focus = 2;
    app.article.as_mut().unwrap().title = "Larger headlines in Current".into();
    let mut terminal = Terminal::new(TestBackend::new(100, 35)).unwrap();
    let mut previous = ratatui::buffer::Buffer::empty(Rect::new(0, 0, 100, 35));
    let mut ansi = Vec::new();
    for state in 0..3 {
        if state == 1 {
            app.show_commands();
        } else {
            app.command_menu = None;
        }
        terminal.draw(|f| app.draw(f)).unwrap();
        let current = terminal.backend().buffer();
        let has_large = current
            .content
            .iter()
            .any(|c| c.symbol().contains("\x1b]66;s=2;"));
        assert_eq!(has_large, state != 1);
        CrosstermBackend::new(&mut ansi)
            .draw(previous.diff_iter(current))
            .unwrap();
        previous = current.clone();
        if let Ok(dir) = std::env::var("CURRENT_TEST_NATIVE_CAPTURE") {
            std::fs::create_dir_all(&dir).unwrap();
            std::fs::write(format!("{dir}/frame-{state}.ansi"), &ansi).unwrap();
        }
    }
    assert!(
        !screen(&mut app, 65, 18).contains("\x1b]66;"),
        "short windows use normal titles"
    );
    key(&mut app, KeyCode::Char('H'));
    assert!(!screen(&mut app, 100, 35).contains("\x1b]66;"));
}

#[test]
fn responsive_navigation_keeps_search_selection_and_scroll() {
    let mut app = app();
    app.items = (1..=30).map(|id| sample(id, false)).collect();
    app.selected = 15;
    app.article = Some(app.items[15].clone());
    app.article.as_mut().unwrap().content =
        "<p>A long article with enough text to scroll through. </p>".repeat(100);
    app.query = "keep this search".into();
    let generation = (app.generation, app.detail_generation);
    let view = screen(&mut app, 80, 25);
    assert!(!view.contains("THE READING ROOM"));
    assert!(!view.contains("COLLECTIONS · unread"));
    assert!(!app.reader_visible);
    let list_offset = app.list_state.offset();

    key(&mut app, KeyCode::Enter);
    assert!(screen(&mut app, 80, 25).contains("READING · Esc headlines"));
    key(&mut app, KeyCode::PageDown);
    screen(&mut app, 80, 25);
    let scroll = app.scroll;
    assert!(scroll > 0);
    key(&mut app, KeyCode::Esc);
    screen(&mut app, 80, 25);
    assert_eq!(app.query, "keep this search");
    assert_eq!(app.selected, 15);
    assert_eq!(app.list_state.offset(), list_offset);
    assert_eq!(app.scroll, scroll);
    key(&mut app, KeyCode::Enter);
    screen(&mut app, 80, 25);
    assert_eq!(app.scroll, scroll);

    for columns in [157, 220, 65, 109, 170, 80] {
        let offset = layout::content_offset(&app.text, app.scroll);
        app.event(Event::Resize(columns, 25));
        let view = screen(&mut app, columns, 25);
        assert!(app.reader_visible);
        assert_eq!(view.contains("COLLECTIONS · unread"), columns >= 170);
        let new_offset = layout::content_offset(&app.text, app.scroll);
        assert!(
            offset.abs_diff(new_offset) < app.text_width as usize,
            "resize must stay within one text line of the previous position"
        );
        assert_eq!(app.article.as_ref().unwrap().id, 16);
    }
    assert_eq!(
        (app.generation, app.detail_generation),
        generation,
        "pane changes and resize must not refetch the article"
    );
}

#[test]
fn sources_are_reachable_and_tab_skips_hidden_sidebar() {
    let mut app = app();
    for columns in [65, 110, 157] {
        app.focus = 1;
        screen(&mut app, columns, 25);
        key(&mut app, KeyCode::Tab);
        assert_eq!(app.focus, 2);
        key(&mut app, KeyCode::BackTab);
        assert_eq!(app.focus, 1);
        key(&mut app, KeyCode::Char('c'));
        assert!(screen(&mut app, columns, 25).contains("COLLECTIONS · unread"));
        assert!(!app.reader_visible);
        key(&mut app, KeyCode::Enter);
        assert_eq!(app.focus, 1);
        key(&mut app, KeyCode::Enter);
        key(&mut app, KeyCode::Char('c'));
        key(&mut app, KeyCode::Esc);
        assert_eq!(app.focus, 2, "closing sources returns to the previous pane");
    }
    screen(&mut app, 220, 25);
    key(&mut app, KeyCode::Tab);
    assert_eq!(app.focus, 0);
    key(&mut app, KeyCode::Tab);
    assert_eq!(app.focus, 1);
    key(&mut app, KeyCode::Char('z'));
    key(&mut app, KeyCode::Char('c'));
    assert!(screen(&mut app, 220, 25).contains("COLLECTIONS · unread"));
    assert!(!app.reader_visible);
    key(&mut app, KeyCode::Esc);
    assert!(app.zen);
    assert!(screen(&mut app, 220, 25).contains("READING · Esc headlines"));
}

#[test]
fn hidden_reader_never_marks_an_article_read() {
    let mut app = app();
    for (width, focus) in [(80, 1), (157, 0), (220, 0), (60, 2)] {
        app.focus = focus;
        app.read_timer = Some((1, Instant::now() - READ_DELAY));
        screen(&mut app, width, 25);
        app.tick_read();
        assert!(!app.mutating, "a hidden or unfocused story cannot be read");
    }
    app.focus = 1;
    screen(&mut app, 80, 25);
    key(&mut app, KeyCode::Enter);
    screen(&mut app, 80, 25);
    app.tick_read();
    assert!(
        !app.mutating,
        "opening must still allow the full reading delay"
    );
    app.read_timer = Some((1, Instant::now() - READ_DELAY));
    app.tick_read();
    assert!(
        app.mutating,
        "a visible story should still mark read after the delay"
    );
}

#[test]
fn revisiting_an_article_before_rendering_preserves_its_position() {
    let mut app = app();
    app.items[0].content = "<p>A paragraph of the article to keep reading.</p>".repeat(100);
    app.article = Some(app.items[0].clone());
    key(&mut app, KeyCode::Enter);
    screen(&mut app, 80, 25);
    key(&mut app, KeyCode::PageDown);
    screen(&mut app, 80, 25);
    let offset = layout::content_offset(&app.text, app.scroll);
    key(&mut app, KeyCode::Esc);
    for code in ['j', 'k', 'j', 'k'] {
        key(&mut app, KeyCode::Char(code));
    }
    assert_eq!(app.pending_position, Some(offset));
    key(&mut app, KeyCode::Enter);
    screen(&mut app, 80, 25);
    assert_eq!(layout::content_offset(&app.text, app.scroll), offset);
}

#[test]
fn image_status_changes_without_resizing_or_reloading_text() {
    let mut app = app();
    app.focus = 2;
    app.article.as_mut().unwrap().image_url = "https://example.com/image.jpg".into();
    app.image_status = "Loading image…".into();
    assert!(screen(&mut app, 80, 40).contains("Loading image…"));
    assert!(!app.text.contains("Loading image"));
    app.tx
        .send(Msg::Image(1, Err("test failure".into())))
        .unwrap();
    app.messages();
    let view = screen(&mut app, 80, 40);
    assert!(!view.contains("Loading image…"));
    assert!(view.contains("Image unavailable"));
    app.tx
        .send(Msg::Image(1, Ok(image::DynamicImage::new_rgb8(200, 100))))
        .unwrap();
    app.messages();
    let view = screen(&mut app, 80, 40);
    assert!(app.picture.is_some());
    assert!(!view.contains("Loading image…"));
    assert!(!view.contains("Image unavailable"));
    assert!(view.contains("End of feed entry."));
}

#[test]
fn help_and_link_picker_work_in_the_smallest_layout() {
    let mut app = app();
    app.article.as_mut().unwrap().content = "<a href='https://example.com'>A link</a>".into();
    screen(&mut app, 65, 18);
    key(&mut app, KeyCode::Char('b'));
    assert!(screen(&mut app, 65, 18).contains("ARTICLE LINKS"));
    assert_eq!(app.links, ["https://example.com"]);
    key(&mut app, KeyCode::Esc);
    key(&mut app, KeyCode::Char('?'));
    assert!(screen(&mut app, 65, 18).contains("Show / hide sources"));
    key(&mut app, KeyCode::End);
    let view = screen(&mut app, 65, 18);
    assert!(view.contains("Reading marks read after 1 second"));
    assert!(view.contains("Esc closes help"));
    key(&mut app, KeyCode::Esc);
    assert!(!app.help);
    assert_eq!(app.focus, 1);
}

#[test]
fn unread_and_saved_are_separate_visible_states() {
    let mut app = app();
    app.items[0].starred = true;
    app.items[1].read = true;
    for (width, height) in [(140, 40), (80, 25)] {
        let view = screen(&mut app, width, height);
        assert!(view.contains("● UNREAD"));
        assert!(view.contains("✓ READ"));
        assert!(view.contains("★ SAVED"));
        assert!(view.contains("Shift+M all read"));
    }
    app.status = 1;
    app.items.clear();
    app.article = None;
    assert!(screen(&mut app, 80, 25).contains("All caught up."));
}

#[test]
#[ignore = "run by the isolated integration harness with a fake browser launcher"]
fn numbered_links_open_from_keyboard() {
    let log = std::env::var("CURRENT_TEST_BROWSER_LOG").expect("test browser launcher required");
    let mut app = app();
    app.status = 1;
    let html = (1..=12)
        .map(|n| {
            format!(
                "<p><a href=\"https://example.com/link/{n}?a=1&amp;b=two#part\">Link {n}</a></p>"
            )
        })
        .collect::<String>();
    app.article.as_mut().unwrap().content = html;
    key(&mut app, KeyCode::Enter);
    let view = screen(&mut app, 80, 25);
    assert!(view.contains("b links"));
    assert_eq!(app.links.len(), 12);
    key(&mut app, KeyCode::Char('b'));
    assert!(app.link_picker.is_some());
    app.read_timer = Some((1, Instant::now() - READ_DELAY));
    app.tick_read();
    assert!(
        !app.mutating,
        "browsing links pauses automatic read marking"
    );
    for size in [(140, 40), (80, 25), (65, 18)] {
        let view = screen(&mut app, size.0, size.1);
        assert!(view.contains("ARTICLE LINKS"));
        assert!(view.contains("Enter open"));
        assert!(view.contains("Esc back"));
    }
    key(&mut app, KeyCode::Char('1'));
    key(&mut app, KeyCode::Char('2'));
    assert_eq!(
        app.status, 1,
        "link numbers must not switch the All/Unread/Saved tabs"
    );
    key(&mut app, KeyCode::Enter);
    assert!(app.link_picker.is_none());
    wait_for_browser(&mut app);
    assert!(app.message.contains("Sent link"));
    let opened = std::fs::read_to_string(&log).unwrap();
    let args: Vec<String> = serde_json::from_str(opened.lines().last().unwrap()).unwrap();
    assert_eq!(args, ["https://example.com/link/12?a=1&b=two#part"]);

    // Full-text replacement must replace the old footnotes as well as the body.
    let mut full = app.article.clone().unwrap();
    full.fulltext = "<a href=\"https://example.com/opener-failure\">New link</a>".into();
    app.tx
        .send(Msg::Fulltext(app.state_version, Ok(full)))
        .unwrap();
    app.messages();
    screen(&mut app, 80, 25);
    assert_eq!(app.links, ["https://example.com/opener-failure"]);
    key(&mut app, KeyCode::Char('b'));
    key(&mut app, KeyCode::Enter);
    wait_for_browser(&mut app);
    assert!(app.message.contains("Could not open link"));

    // Returning to an article with no links cannot reuse another story's table.
    app.article.as_mut().unwrap().content = "<p>No links here.</p>".into();
    app.article.as_mut().unwrap().fulltext.clear();
    app.text_width = 0;
    screen(&mut app, 80, 25);
    assert!(app.links.is_empty());
    key(&mut app, KeyCode::Char('b'));
    assert!(app.link_picker.is_none());
    assert!(app.message.contains("no numbered links"));
}

fn wait_for_browser(app: &mut App) {
    let deadline = Instant::now() + Duration::from_secs(5);
    while app.message == "Opening link in your browser…" {
        app.messages();
        assert!(Instant::now() < deadline, "browser opener timed out");
        thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn finished_story_stays_until_navigation_without_skipping_next() {
    let mut app = app();
    app.status = 1;
    app.apply_state(&sample(1, true));
    assert_eq!(app.article.as_ref().unwrap().id, 1);
    assert_eq!(app.items.len(), 3, "keep the story still being read");
    key(&mut app, KeyCode::Char('j'));
    assert_eq!(app.article.as_ref().unwrap().id, 2);
    assert_eq!(app.items.iter().map(|a| a.id).collect::<Vec<_>>(), [2, 3]);
    // An earlier story's delayed save must not move the currently selected one.
    app.apply_state(&sample(1, true));
    assert_eq!(app.article.as_ref().unwrap().id, 2);
    app.apply_state(&sample(2, true));
    key(&mut app, KeyCode::Char('j'));
    assert_eq!(app.article.as_ref().unwrap().id, 3);
    app.apply_state(&sample(3, true));
    key(&mut app, KeyCode::Char('j'));
    assert!(app.items.is_empty());
    assert!(app.article.is_none());
}

#[test]
fn stale_detail_poll_and_counts_cannot_undo_a_read() {
    let mut app = app();
    app.state_version = 1;
    app.mutating = true;
    app.tx
        .send(Msg::Mutation(
            Ok(sample(1, true)),
            Some(sample(1, false)),
            false,
        ))
        .unwrap();
    app.tx
        .send(Msg::Counts(
            2,
            Ok(Counts {
                total: 2,
                ..Counts::default()
            }),
        ))
        .unwrap();
    app.tx.send(Msg::State(0, Ok(sample(1, false)))).unwrap();
    app.tx
        .send(Msg::Article(app.detail_generation, 0, Ok(sample(1, false))))
        .unwrap();
    app.tx.send(Msg::Fulltext(0, Ok(sample(1, false)))).unwrap();
    app.tx
        .send(Msg::Counts(
            0,
            Ok(Counts {
                total: 3,
                ..Counts::default()
            }),
        ))
        .unwrap();
    app.messages();
    assert!(app.article.as_ref().unwrap().read);
    assert!(app.items[0].read);
    assert_eq!(app.counts.total, 2);
    assert!(app.read_timer.is_none());
    app.tx
        .send(Msg::List(
            app.generation,
            0,
            Ok(Page {
                items: vec![sample(1, false)],
                next: String::new(),
            }),
            false,
        ))
        .unwrap();
    app.messages();
    assert!(
        app.items[0].read,
        "an older list response must also preserve the new state"
    );
    assert!(app.article.as_ref().unwrap().read);
}

#[test]
fn failed_read_keeps_the_article_unread_and_reports_the_error() {
    let mut app = app();
    app.status = 1;
    app.state_version = 1;
    app.mutating = true;
    app.tx
        .send(Msg::Mutation(
            Err("server unavailable".into()),
            Some(sample(1, false)),
            false,
        ))
        .unwrap();
    app.messages();
    assert!(!app.article.as_ref().unwrap().read);
    assert!(!app.items[0].read);
    assert!(app.undo.is_empty());
    assert!(app.message.contains("server unavailable"));
}

#[test]
fn auto_read_waits_for_visible_reading_and_respects_keep_unread() {
    let mut app = app();
    // Reading must pause in the source pane, dialogs, and undersized windows.
    for mode in 0..5 {
        app.focus = if mode == 0 { 0 } else { 1 };
        app.help = mode == 1;
        app.searching = mode == 2;
        app.reader_visible = mode != 3;
        app.mark_all = (mode == 4).then(|| app.sources[0].clone());
        app.read_timer = Some((1, Instant::now() - READ_DELAY));
        app.tick_read();
        assert!(!app.mutating);
    }
    app.mark_all = None;
    app.keep_unread = Some(1);
    app.read_timer = Some((1, Instant::now() - READ_DELAY));
    app.tick_read();
    assert!(!app.mutating);
    // A timer for a story passed over during rapid navigation is not a read.
    app.keep_unread = None;
    app.read_timer = Some((2, Instant::now() - READ_DELAY));
    app.tick_read();
    assert!(!app.mutating);
}

#[test]
fn bulk_confirmation_names_scope_and_can_be_cancelled() {
    let mut app = app();
    let source = Source {
        title: "Radio".into(),
        feed: Some(10),
        category: Some(3),
    };
    assert_eq!(source.mark_read_body(), json!({"feedId": 10}));
    app.sources.push(source);
    app.source = 1;
    key(&mut app, KeyCode::Char('M'));
    let view = screen(&mut app, 80, 25);
    assert!(view.contains("feed: Radio"));
    assert!(view.contains("every page"));
    key(&mut app, KeyCode::Esc);
    assert!(app.mark_all.is_none());
    assert!(!app.mutating);
    assert!(app.items.iter().all(|a| !a.read));
    app.sources[1].feed = None;
    assert_eq!(app.sources[1].mark_read_body(), json!({"categoryId": 3}));
    assert_eq!(app.sources[0].mark_read_body(), json!({}));
}

fn settle(app: &mut App) {
    let deadline = Instant::now() + Duration::from_secs(10);
    loop {
        app.messages();
        if !app.loading
            && !app.mutating
            && (app.items.is_empty()
                || app.read_timer.is_some()
                || app.article.as_ref().is_some_and(|a| a.read)
                || app.keep_unread.is_some())
        {
            return;
        }
        assert!(
            Instant::now() < deadline,
            "reader failed to settle: {}",
            app.message
        );
        thread::sleep(Duration::from_millis(10));
    }
}
fn unread(base: &str, id: i64) -> usize {
    get::<Counts>(base, "counts")
        .unwrap()
        .feeds
        .get(&id.to_string())
        .copied()
        .unwrap_or(0)
}

#[test]
#[ignore = "run by the isolated browser/integration harness; changes test reading state"]
fn reading_and_bulk_actions_round_trip() {
    // Intentionally require harness URLs; never default this writing test to the user's server.
    let base = std::env::var("CURRENT_TEST_URL").expect("isolated server URL required");
    let fixture = std::env::var("CURRENT_TEST_READING_FEED").expect("isolated feed URL required");
    let category = manage::request(
        &base,
        "POST",
        "categories",
        Some(json!({"title":"Reading test"})),
        None,
    )
    .unwrap()["id"]
        .as_i64()
        .unwrap();
    let mut ids = vec![];
    for name in ["primary", "sibling", "outside"] {
        let v = manage::request(
            &base,
            "POST",
            "feeds",
            Some(json!({
                "url":format!("{fixture}/{name}"), "direct":true, "title":name,
                "categoryId":if name == "outside" { None } else { Some(category) },
            })),
            None,
        )
        .unwrap();
        ids.push(v["id"].as_i64().unwrap());
    }
    let mut app = App::new(base.clone(), Picker::halfblocks()).unwrap();
    settle(&mut app);
    app.show_feeds = true;
    app.rebuild_sources();
    let index = app
        .sources
        .iter()
        .position(|s| s.feed == Some(ids[0]))
        .unwrap();
    app.status = 1;
    app.select_source(index);
    settle(&mut app);
    assert_eq!(app.items.len(), 100);
    assert!(!app.next.is_empty());
    let first = app.article.as_ref().unwrap().id;
    let second = app.items[1].id;
    let saved_count = get::<Counts>(&base, "counts").unwrap().starred;
    app.reader_visible = true;
    app.read_timer = Some((first, Instant::now() - READ_DELAY));
    app.tick_read();
    settle(&mut app);
    assert!(
        get::<Article>(&base, &format!("articles/{first}"))
            .unwrap()
            .read
    );
    assert_eq!(unread(&base, ids[0]), 124);
    assert_eq!(app.article.as_ref().unwrap().id, first);
    key(&mut app, KeyCode::Char('u'));
    settle(&mut app);
    assert!(
        !get::<Article>(&base, &format!("articles/{first}"))
            .unwrap()
            .read
    );
    assert!(
        app.read_timer.is_none(),
        "undo must not immediately auto-read again"
    );
    key(&mut app, KeyCode::Char('s'));
    settle(&mut app);
    key(&mut app, KeyCode::Char('m'));
    settle(&mut app);
    key(&mut app, KeyCode::Char('j'));
    settle(&mut app);
    assert_eq!(app.article.as_ref().unwrap().id, second);
    assert!(!app.items.iter().any(|a| a.id == first));
    key(&mut app, KeyCode::Char('M'));
    key(&mut app, KeyCode::Esc);
    assert_eq!(unread(&base, ids[0]), 124);
    // Even an empty search must mark the named whole feed, not just the visible page.
    app.query = "no-matching-story".into();
    app.load(false);
    settle(&mut app);
    assert!(app.items.is_empty());
    key(&mut app, KeyCode::Char('M'));
    key(&mut app, KeyCode::Enter);
    settle(&mut app);
    assert_eq!(unread(&base, ids[0]), 0);
    assert_eq!(
        unread(&base, ids[1]),
        125,
        "feed action must not touch siblings"
    );
    assert_eq!(unread(&base, ids[2]), 125);
    assert!(
        get::<Article>(&base, &format!("articles/{first}"))
            .unwrap()
            .starred
    );
    assert_eq!(
        get::<Counts>(&base, "counts").unwrap().starred,
        saved_count + 1
    );
    app.query.clear();
    app.show_feeds = false;
    app.rebuild_sources();
    let index = app
        .sources
        .iter()
        .position(|s| s.category == Some(category))
        .unwrap();
    app.select_source(index);
    settle(&mut app);
    key(&mut app, KeyCode::Char('M'));
    key(&mut app, KeyCode::Enter);
    settle(&mut app);
    assert_eq!(unread(&base, ids[1]), 0);
    assert_eq!(
        unread(&base, ids[2]),
        125,
        "collection action must not touch other collections"
    );
    assert!(app.items.is_empty());
    app.select_source(0);
    settle(&mut app);
    key(&mut app, KeyCode::Char('M'));
    key(&mut app, KeyCode::Enter);
    settle(&mut app);
    assert_eq!(get::<Counts>(&base, "counts").unwrap().total, 0);
    assert!(app.items.is_empty());
    assert!(
        get::<Article>(&base, &format!("articles/{first}"))
            .unwrap()
            .starred
    );
}
