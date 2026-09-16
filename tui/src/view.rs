use super::*;
use layout::{Mode, ellipsis, title_lines, width};

fn single_line(text: &str) -> String {
    clean(text).split_whitespace().collect::<Vec<_>>().join(" ")
}

fn metadata(a: &Article, available: usize, colors: Palette) -> Line<'static> {
    let state = if a.read { "✓ READ" } else { "● UNREAD" };
    let saved = if a.starred { " ★ SAVED" } else { "" };
    let when = if a.published_at.is_empty() {
        String::new()
    } else {
        format!(" · {}", date(&a.published_at))
    };
    let source_width = available.saturating_sub(width(state) + width(saved) + width(&when) + 3);
    Line::from(vec![
        Span::styled(
            state,
            if a.read {
                Style::default().fg(colors.muted)
            } else {
                Style::default().fg(colors.accent).bold()
            },
        ),
        Span::styled(saved, Style::default().fg(colors.accent)),
        Span::styled(
            format!(
                " · {}{}",
                ellipsis(&single_line(&a.feed_title), source_width),
                when
            ),
            Style::default().fg(colors.muted),
        ),
    ])
}

fn article_row(a: &Article, available: usize, colors: Palette) -> ListItem<'static> {
    let mut lines = vec![metadata(a, available, colors)];
    for text in title_lines(&clean(&a.title), available, 3) {
        lines.push(Line::styled(
            text,
            if a.read {
                Style::default().fg(colors.muted)
            } else {
                Style::default().fg(colors.text).bold()
            },
        ));
    }
    lines.push(Line::from(""));
    ListItem::new(lines)
}

impl App {
    pub(super) fn draw(&mut self, f: &mut Frame) {
        self.theme.poll();
        let colors = self.theme.colors;
        let area = f.area();
        self.layout_mode = Mode::for_width(area.width);
        self.reader_visible = false;
        if let Some(manager) = &mut self.manager {
            manager.draw(f, colors);
            return;
        }
        f.render_widget(
            Block::default().style(Style::default().bg(colors.bg).fg(colors.text)),
            area,
        );
        if area.width < 65 || area.height < 18 {
            f.render_widget(Paragraph::new("Current needs at least 65 columns × 18 rows.\nResize the window, or press Q to exit.").wrap(Wrap { trim: false }), area);
            return;
        }
        if let Some(gallery) = &mut self.gallery {
            gallery.draw(f, &self.picker, colors);
            return;
        }
        let sections = Layout::vertical([
            Constraint::Length(2),
            Constraint::Min(5),
            Constraint::Length(2),
        ])
        .split(area);
        self.draw_header(f, sections[0], colors);
        let parts = self.layout_mode.panes(sections[1], self.focus, self.zen);
        if let Some(rect) = parts[0] {
            self.draw_sources(f, rect, colors);
        }
        if let Some(rect) = parts[1] {
            self.draw_headlines(f, rect, colors);
        }
        if let Some(rect) = parts[2] {
            self.reader_visible = true;
            self.draw_reader(f, rect, colors);
        }
        self.draw_footer(f, sections[2], colors);
        if self.help {
            self.draw_help(f, colors);
        }
        if let Some(source) = &self.mark_all {
            let popup = Rect::new(
                area.x + area.width.saturating_sub(64) / 2,
                area.y + area.height.saturating_sub(15) / 2,
                64.min(area.width),
                15.min(area.height),
            );
            let block = panel(" MARK ALL READ ", true, colors);
            let inner = block.inner(popup).inner(Margin::new(1, 0));
            let parts = Layout::vertical([Constraint::Min(1), Constraint::Length(2)]).split(inner);
            f.render_widget(Clear, popup);
            f.render_widget(block, popup);
            let text = format!(
                "Mark all unread articles as read in\n{}?\n\n{} unread at last sync. Includes every page, even outside the current search or Saved filter.\n\nArticles and saved items are kept.\nThis action cannot be undone with U.",
                source.scope_label(),
                source.unread(&self.counts)
            );
            f.render_widget(
                Paragraph::new(text)
                    .wrap(Wrap { trim: false })
                    .style(Style::default().fg(colors.text)),
                parts[0],
            );
            f.render_widget(
                Paragraph::new("Y / Enter marks read · N / Esc cancels")
                    .style(Style::default().fg(colors.accent)),
                parts[1],
            );
        }
        if let Some(picker) = &mut self.link_picker {
            picker.draw(f, colors);
        }
        if let Some(menu) = &mut self.command_menu {
            menu.draw(f, colors);
        }
    }

    fn draw_header(&self, f: &mut Frame, rect: Rect, colors: Palette) {
        let lines = Layout::vertical([Constraint::Length(1), Constraint::Length(1)]).split(rect);
        let top = Layout::horizontal([
            Constraint::Length(12),
            Constraint::Min(1),
            Constraint::Length(18),
        ])
        .split(lines[0]);
        f.render_widget(
            Paragraph::new(" ◉ current").style(Style::default().fg(colors.accent).bold()),
            top[0],
        );
        f.render_widget(
            Paragraph::new(ellipsis(&self.refresh.label(), top[1].width as usize))
                .style(Style::default().fg(colors.muted))
                .alignment(Alignment::Left),
            top[1],
        );
        f.render_widget(
            Paragraph::new("Ctrl+P commands")
                .style(Style::default().fg(colors.accent))
                .alignment(Alignment::Right),
            top[2],
        );
        let tabs = [
            " 1 All ".into(),
            format!(" 2 Unread {} ", self.counts.total),
            format!(" 3 Saved {} ", self.counts.starred),
        ];
        let tabs_width = tabs.iter().map(|s| width(s)).sum::<usize>() as u16;
        let lower = Layout::horizontal([Constraint::Length(tabs_width), Constraint::Min(1)])
            .split(lines[1]);
        f.render_widget(
            Paragraph::new(Line::from(
                tabs.into_iter()
                    .enumerate()
                    .map(|(n, text)| {
                        Span::styled(
                            text,
                            if n == self.status {
                                Style::default().fg(colors.accent).bold()
                            } else {
                                Style::default().fg(colors.muted)
                            },
                        )
                    })
                    .collect::<Vec<_>>(),
            )),
            lower[0],
        );
        let search = if self.searching {
            format!("Search: {}▏", self.query)
        } else if self.query.is_empty() {
            "/ search".into()
        } else {
            format!("/ {}", self.query)
        };
        f.render_widget(
            Paragraph::new(layout::tail(
                &search,
                lower[1].width.saturating_sub(1) as usize,
            ))
            .style(Style::default().fg(if self.searching {
                colors.text
            } else {
                colors.muted
            }))
            .alignment(Alignment::Right),
            lower[1],
        );
    }

    fn draw_sources(&mut self, f: &mut Frame, rect: Rect, colors: Palette) {
        let available = rect.width.saturating_sub(4) as usize;
        let rows = self
            .sources
            .iter()
            .map(|s| {
                let count = s.unread(&self.counts).to_string();
                let name_width = available.saturating_sub(width(&count) + 2);
                let name = ellipsis(&single_line(&s.title), name_width);
                let padding = name_width.saturating_sub(width(&name)) + 2;
                ListItem::new(Line::from(vec![
                    Span::raw(name),
                    Span::styled(
                        format!("{}{count}", " ".repeat(padding)),
                        Style::default().fg(colors.muted),
                    ),
                ]))
            })
            .collect::<Vec<_>>();
        let title = if self.show_feeds {
            " FEEDS · unread "
        } else {
            " COLLECTIONS · unread "
        };
        self.source_state.select(Some(self.source));
        f.render_stateful_widget(
            List::new(rows)
                .block(panel(title, self.focus == 0, colors))
                .highlight_style(Style::default().bg(colors.selection).fg(colors.accent))
                .highlight_symbol("▎"),
            rect,
            &mut self.source_state,
        );
    }

    fn draw_headlines(&mut self, f: &mut Frame, rect: Rect, colors: Palette) {
        let rows = self
            .items
            .iter()
            .map(|a| article_row(a, rect.width.saturating_sub(4) as usize, colors))
            .collect::<Vec<_>>();
        let suffix = if self.loading {
            " · loading".into()
        } else {
            format!(
                " · {} unread",
                self.sources[self.source].unread(&self.counts)
            )
        };
        let title = format!(
            " {}{} ",
            ellipsis(
                &single_line(&self.list_title),
                rect.width.saturating_sub(4) as usize
                    - width(&suffix).min(rect.width.saturating_sub(4) as usize)
            ),
            suffix
        );
        self.list_state
            .select((!self.items.is_empty()).then_some(self.selected));
        f.render_stateful_widget(
            List::new(rows)
                .block(panel(&title, self.focus == 1, colors))
                .highlight_style(Style::default().bg(colors.selection))
                .highlight_symbol("▎"),
            rect,
            &mut self.list_state,
        );
        if self.items.is_empty() {
            let text = if self.loading {
                "Loading your stories…"
            } else if self.status == 1 && self.query.is_empty() {
                "All caught up.\nNo unread articles here.\n\n1 shows read articles too."
            } else {
                "No articles in this view.\nA adds a feed · G manages sources."
            };
            f.render_widget(
                Paragraph::new(text)
                    .style(Style::default().fg(colors.muted))
                    .wrap(Wrap { trim: false }),
                rect.inner(Margin::new(2, 2)),
            );
        }
    }

    fn draw_footer(&self, f: &mut Frame, rect: Rect, colors: Palette) {
        let keys = if self.searching {
            "Enter search · Esc cancel"
        } else if self.focus == 0 {
            "j/k select  Enter headlines  Esc back  f feeds/collections  ? keys"
        } else if self.focus == 2 || self.zen {
            "[ ] stories  j/k scroll  v images  b links  Esc list  ? keys"
        } else {
            "j/k select  Enter read  c sources  Shift+M all read  ? keys"
        };
        let notice = if self.message_time.elapsed() < Duration::from_secs(8) {
            self.message.as_str()
        } else {
            "s save · m read/unread · r refresh · Ctrl+P commands · q quit"
        };
        f.render_widget(
            Paragraph::new(vec![
                Line::styled(
                    ellipsis(keys, rect.width as usize),
                    Style::default().fg(colors.accent),
                ),
                Line::styled(
                    ellipsis(notice, rect.width as usize),
                    Style::default().fg(colors.muted),
                ),
            ]),
            rect,
        );
    }

    fn draw_help(&mut self, f: &mut Frame, colors: Palette) {
        let text = "J / K or arrows    Move in the focused pane\n[ / ]              Previous / next article\nCtrl+P             Search commands\nV                  Image viewer: zoom, pan, gallery\nShift+H            Toggle large reader headlines\nEnter              Open article / accept source\nEsc                Back to headlines / close sources\nC                  Show / hide sources\nTab / Shift-Tab    Change visible pane\n1 / 2 / 3          All / unread / saved\n/                  Search; Enter applies\nS                  Save / unsave article\nM                  Toggle read / unread\nShift+M            Mark all read in this source\nU                  Undo last reading-state action\nZ                  Focus / leave the reader\nT                  Load full article text\nI                  Toggle image preview\nF                  Switch collections / individual feeds\nPgUp / PgDn        Scroll article\nN                  Load next page of headlines\nO                  Open original in your browser\nB                  Browse numbered links\nG / A              Manage feeds / add a source\nR / Shift+R        Refresh feeds and update headlines\nCtrl+R             Reload cached stories\nQ                  Quit\n\nReading marks read after 1 second on screen.\nUnread stories leave the list when you move on.";
        let area = f.area();
        let popup = Rect::new(
            area.x + area.width.saturating_sub(65) / 2,
            area.y + area.height.saturating_sub(31) / 2,
            65.min(area.width),
            31.min(area.height),
        );
        let block = panel(" KEYBOARD HELP ", true, colors);
        let parts = Layout::vertical([Constraint::Min(1), Constraint::Length(1)])
            .split(block.inner(popup).inner(Margin::new(1, 0)));
        self.help_scroll = self.help_scroll.min(
            text.lines()
                .count()
                .saturating_sub(parts[0].height as usize) as u16,
        );
        f.render_widget(Clear, popup);
        f.render_widget(block, popup);
        f.render_widget(
            Paragraph::new(text)
                .style(Style::default().fg(colors.text))
                .scroll((self.help_scroll, 0)),
            parts[0],
        );
        f.render_widget(
            Paragraph::new("j/k or PgUp/PgDn scroll · Esc closes help")
                .style(Style::default().fg(colors.accent)),
            parts[1],
        );
    }

    pub(super) fn ensure_text(&mut self, available: u16) {
        if self.text_width == available {
            return;
        }
        let Some(a) = &self.article else { return };
        let offset = self
            .pending_position
            .unwrap_or_else(|| layout::content_offset(&self.text, self.scroll));
        // List responses contain only summaries. Keep the saved anchor until
        // the complete article arrives, even if a short preview renders first.
        if !self.detail_loading {
            self.pending_position = None;
        }
        let html = if !a.fulltext.is_empty() {
            &a.fulltext
        } else if !a.content.is_empty() {
            &a.content
        } else {
            &a.summary
        };
        (self.text, self.links) = links::render(html, available.max(10) as usize)
            .unwrap_or_else(|_| (clean(&a.summary), vec![]));
        let ending = if a.fulltext.is_empty() {
            "End of feed entry. T loads full text · O opens original."
        } else {
            "End of full text. O opens original."
        };
        self.text.push_str(&format!(
            "\n\n──────────────────\n{}\n",
            wrap_text(ending, available as usize)
        ));
        if !self.links.is_empty() {
            self.text.push_str(&wrap_text(
                "B opens numbered links: number, then Enter.",
                available as usize,
            ));
        }
        self.scroll = layout::scroll_for_offset(&self.text, offset);
        self.text_width = available;
    }

    fn draw_reader(&mut self, f: &mut Frame, rect: Rect, colors: Palette) {
        let block = panel(
            if self.layout_mode == Mode::Thin || self.zen {
                " READING · Esc headlines "
            } else {
                " THE READING ROOM "
            },
            self.focus == 2 || self.zen,
            colors,
        );
        let inner = block.inner(rect).inner(Margin::new(2, 1));
        f.render_widget(block, rect);
        let Some(a) = self.article.clone() else {
            f.render_widget(
                Paragraph::new("Select a story from your collection.")
                    .style(Style::default().fg(colors.muted))
                    .wrap(Wrap { trim: false }),
                inner,
            );
            return;
        };
        let large = self.text_sizing && self.large_headlines && inner.height >= 18;
        let factor = if large { 2 } else { 1 };
        let title = title_lines(
            &clean(&a.title),
            inner.width as usize / factor,
            (if large { 4 } else { 5 })
                .min(inner.height.saturating_sub(8) as usize / factor)
                .max(1),
        );
        let title_height = title.len() as u16 * factor as u16;
        let overlay = self.help
            || self.mark_all.is_some()
            || self.link_picker.is_some()
            || self.command_menu.is_some();
        let mut header = vec![metadata(&a, inner.width as usize, colors), Line::from("")];
        if large {
            header.extend((0..title_height).map(|_| Line::from("")));
        } else {
            header.extend(
                title.iter().map(|line| {
                    Line::styled(line.clone(), Style::default().fg(colors.text).bold())
                }),
            );
        }
        if !a.author.is_empty() {
            header.push(Line::styled(
                ellipsis(
                    &format!("By {}", single_line(&a.author)),
                    inner.width as usize,
                ),
                Style::default().fg(colors.muted),
            ));
        }
        header.push(Line::from(""));
        let header_height = header.len() as u16;
        let remaining = inner.height.saturating_sub(header_height);
        let image_size = self
            .picture
            .as_ref()
            .filter(|_| self.images && remaining >= 12)
            .map(|pic| {
                pic.size_for(
                    Resize::Fit(None),
                    Size::new(inner.width, (remaining / 3).min(24)),
                )
            });
        let image_height = image_size.map(|s| s.height + 1).unwrap_or(0);
        let status_height = u16::from(
            self.images
                && self.picture.is_none()
                && !a.image_url.is_empty()
                && !self.image_status.is_empty(),
        );
        let parts = Layout::vertical([
            Constraint::Length(header_height),
            Constraint::Length(image_height),
            Constraint::Length(status_height),
            Constraint::Min(1),
        ])
        .split(inner);
        f.render_widget(Paragraph::new(header), parts[0]);
        if large {
            let rect = Rect::new(inner.x, inner.y + 2, inner.width, title_height);
            let style = Style::default().fg(colors.text).bg(colors.panel).bold();
            if overlay {
                f.render_widget(Paragraph::new(title.join("\n")).style(style), rect);
            } else {
                f.render_widget(
                    headline::Headline {
                        lines: &title,
                        style,
                    },
                    rect,
                );
            }
        }
        if let (Some(size), Some(pic)) = (image_size, &mut self.picture) {
            let r = Rect::new(
                parts[1].x + parts[1].width.saturating_sub(size.width) / 2,
                parts[1].y,
                size.width,
                size.height,
            );
            if !overlay {
                f.render_stateful_widget(
                    StatefulImage::default().resize({
                        // Native-size images need padding, not cell-rounded scaling.
                        let natural =
                            pic.size_for(Resize::Crop(None), Size::new(u16::MAX, u16::MAX));
                        if natural.width <= r.width && natural.height <= r.height {
                            Resize::Crop(None)
                        } else {
                            Resize::Fit(Some(image::imageops::FilterType::Lanczos3))
                        }
                    }),
                    r,
                    pic,
                );
            }
        }
        if status_height > 0 {
            f.render_widget(
                Paragraph::new(ellipsis(&self.image_status, parts[2].width as usize))
                    .style(Style::default().fg(colors.muted)),
                parts[2],
            );
        }
        self.ensure_text(parts[3].width);
        let max_scroll = self
            .text
            .lines()
            .count()
            .saturating_sub(parts[3].height as usize)
            .min(u16::MAX as usize) as u16;
        self.scroll = self.scroll.min(max_scroll);
        f.render_widget(
            Paragraph::new(self.text.as_str())
                .style(Style::default().fg(colors.text))
                .scroll((self.scroll, 0)),
            parts[3],
        );
    }
}
