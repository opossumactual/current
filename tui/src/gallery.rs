use crate::{Palette, Result, get, layout, panel};
use crossterm::event::KeyCode;
use image::{DynamicImage, ImageReader};
use ratatui::{
    prelude::*,
    widgets::{Paragraph, Wrap},
};
use ratatui_image::{Resize, StatefulImage, picker::Picker, protocol::StatefulProtocol};
use serde::Deserialize;
use std::{
    io::Cursor,
    sync::mpsc::{self, Receiver, Sender},
    thread,
    time::Duration,
};

#[derive(Clone, Deserialize)]
pub struct Entry {
    pub url: String,
    pub caption: String,
}
enum Reply {
    Entries(Result<Vec<Entry>>),
    Image(u64, Result<DynamicImage>),
}
pub enum Action {
    Stay,
    Close,
    Open(String),
}
#[derive(Clone, Copy, Debug, PartialEq)]
enum Zoom {
    Fit,
    Actual,
    Manual(f64),
}
impl Zoom {
    fn scale(self, image: (u32, u32), viewport: (u32, u32)) -> f64 {
        match self {
            Self::Fit => (viewport.0.max(1) as f64 / image.0.max(1) as f64)
                .min(viewport.1.max(1) as f64 / image.1.max(1) as f64)
                .min(1.),
            Self::Actual => 1.,
            Self::Manual(scale) => scale,
        }
    }
}
pub struct Gallery {
    base: String,
    id: i64,
    title: String,
    tx: Sender<Reply>,
    rx: Receiver<Reply>,
    pub entries: Vec<Entry>,
    pub selected: usize,
    revision: u64,
    loading: bool,
    message: String,
    decoded: Option<DynamicImage>,
    picture: Option<StatefulProtocol>,
    zoom: Zoom,
    pixels: (u32, u32),
    center: (f64, f64),
    viewport: Rect,
    crop: (u32, u32),
    dirty: bool,
}
pub fn fetch_image(base: &str, id: i64, index: usize) -> Result<DynamicImage> {
    let bytes = ureq::get(format!("{base}/api/image/{id}?index={index}"))
        .config()
        .timeout_global(Some(Duration::from_secs(25)))
        .build()
        .call()
        .map_err(|e| e.to_string())?
        .body_mut()
        .with_config()
        .limit(8 * 1024 * 1024)
        .read_to_vec()
        .map_err(|e| e.to_string())?;
    let mut reader = ImageReader::new(Cursor::new(bytes))
        .with_guessed_format()
        .map_err(|e| e.to_string())?;
    let mut limits = image::Limits::default();
    limits.max_image_width = Some(16000);
    limits.max_image_height = Some(16000);
    limits.max_alloc = Some(128 * 1024 * 1024);
    reader.limits(limits);
    reader.decode().map_err(|e| e.to_string())
}
impl Gallery {
    pub fn new(base: String, id: i64, title: String) -> Self {
        let (tx, rx) = mpsc::channel();
        let mut this = Self {
            base,
            id,
            title,
            tx,
            rx,
            entries: vec![],
            selected: 0,
            revision: 0,
            loading: true,
            message: String::new(),
            decoded: None,
            picture: None,
            zoom: Zoom::Fit,
            pixels: (1, 1),
            center: (0.5, 0.5),
            viewport: Rect::default(),
            crop: (0, 0),
            dirty: true,
        };
        this.load_entries();
        this
    }
    fn load_entries(&mut self) {
        self.loading = true;
        self.message = "Finding article images…".into();
        let (tx, base, id) = (self.tx.clone(), self.base.clone(), self.id);
        thread::spawn(move || {
            let _ = tx.send(Reply::Entries(get(&base, &format!("articles/{id}/images"))));
        });
    }
    fn load_image(&mut self) {
        self.revision += 1;
        self.loading = true;
        self.message = "Loading image…".into();
        self.decoded = None;
        self.picture = None;
        self.zoom = Zoom::Fit;
        self.center = (0.5, 0.5);
        self.dirty = true;
        let (tx, base, id, index, revision) = (
            self.tx.clone(),
            self.base.clone(),
            self.id,
            self.selected,
            self.revision,
        );
        thread::spawn(move || {
            let _ = tx.send(Reply::Image(revision, fetch_image(&base, id, index)));
        });
    }
    pub fn tick(&mut self) {
        while let Ok(reply) = self.rx.try_recv() {
            match reply {
                Reply::Entries(result) => match result {
                    Ok(entries) => {
                        self.entries = entries;
                        if self.entries.is_empty() {
                            self.loading = false;
                            self.message="No images in the loaded article. Esc returns to reading; T can load full text.".into();
                        } else {
                            self.load_image();
                        }
                    }
                    Err(_) => {
                        self.loading = false;
                        self.message =
                            "Could not load the image list. R retries · Esc returns.".into();
                    }
                },
                Reply::Image(revision, result) if revision == self.revision => {
                    self.loading = false;
                    match result {
                        Ok(img) => {
                            self.decoded = Some(img);
                            self.dirty = true;
                            self.message.clear();
                        }
                        Err(_) => {
                            self.message =
                                "Image unavailable. R retries · O opens image in browser.".into()
                        }
                    }
                }
                _ => {}
            }
        }
    }
    pub fn key(&mut self, key: KeyCode) -> Action {
        match key {
            KeyCode::Esc | KeyCode::Char('q' | 'v') => return Action::Close,
            KeyCode::Char('o') => {
                if let Some(entry) = self.entries.get(self.selected) {
                    return Action::Open(entry.url.clone());
                }
            }
            KeyCode::Char(']') => {
                if self.selected + 1 < self.entries.len() {
                    self.selected += 1;
                    self.load_image();
                }
            }
            KeyCode::Char('[') => {
                if self.selected > 0 {
                    self.selected -= 1;
                    self.load_image();
                }
            }
            KeyCode::Char('+' | '=') => {
                self.change_zoom(1.5);
            }
            KeyCode::Char('-') => {
                self.change_zoom(1. / 1.5);
            }
            KeyCode::Char('0') | KeyCode::Home => {
                self.zoom = Zoom::Fit;
                self.center = (0.5, 0.5);
                self.dirty = true;
            }
            KeyCode::Char('1') => {
                self.zoom = Zoom::Actual;
                self.dirty = true;
            }
            KeyCode::Left | KeyCode::Char('h') => self.pan(-1., 0.),
            KeyCode::Right | KeyCode::Char('l') => self.pan(1., 0.),
            KeyCode::Up | KeyCode::Char('k') => self.pan(0., -1.),
            KeyCode::Down | KeyCode::Char('j') => self.pan(0., 1.),
            KeyCode::Char('r') if !self.loading => {
                if self.entries.is_empty() {
                    self.load_entries()
                } else {
                    self.load_image()
                }
            }
            _ => {}
        }
        Action::Stay
    }
    fn change_zoom(&mut self, factor: f64) {
        if let Some(img) = &self.decoded {
            let dimensions = (img.width(), img.height());
            let fit = Zoom::Fit.scale(dimensions, self.pixels);
            let scale = (self.zoom.scale(dimensions, self.pixels) * factor).min(16.);
            self.zoom = if scale <= fit {
                Zoom::Fit
            } else {
                Zoom::Manual(scale)
            };
            self.dirty = true;
        }
    }
    fn pan(&mut self, x: f64, y: f64) {
        if let Some(img) = &self.decoded {
            self.center.0 += (x * self.crop.0 as f64 * 0.15) / img.width() as f64;
            self.center.1 += (y * self.crop.1 as f64 * 0.15) / img.height() as f64;
            self.dirty = true;
        }
    }
    pub fn draw(&mut self, f: &mut Frame, picker: &Picker, colors: Palette) {
        let area = f.area();
        let block = panel(" IMAGE VIEWER ", true, colors);
        let parts = Layout::vertical([
            Constraint::Length(2),
            Constraint::Min(1),
            Constraint::Length(3),
            Constraint::Length(2),
        ])
        .split(block.inner(area).inner(Margin::new(1, 0)));
        f.render_widget(block, area);
        let font = picker.font_size();
        let pixels = (
            u32::from(parts[1].width) * u32::from(font.width),
            u32::from(parts[1].height) * u32::from(font.height),
        );
        if parts[1] != self.viewport || pixels != self.pixels {
            self.viewport = parts[1];
            self.pixels = pixels;
            self.dirty = true;
        }
        let details = if let Some(img) = &self.decoded {
            let percent = 100. * self.zoom.scale((img.width(), img.height()), pixels);
            let zoom = match self.zoom {
                Zoom::Fit => format!("Fit ({percent:.0}%)"),
                Zoom::Actual => "100% · actual pixels".into(),
                Zoom::Manual(_) => format!("{percent:.0}%"),
            };
            format!(" · {} × {} px · {zoom}", img.width(), img.height())
        } else {
            String::new()
        };
        let status = format!(
            "{}/{}{}",
            self.selected + usize::from(!self.entries.is_empty()),
            self.entries.len(),
            details
        );
        f.render_widget(
            Paragraph::new(vec![
                Line::styled(
                    layout::ellipsis(&self.title, parts[0].width as usize),
                    Style::default().fg(colors.text).bold(),
                ),
                Line::styled(
                    layout::ellipsis(&status, parts[0].width as usize),
                    Style::default().fg(colors.muted),
                ),
            ]),
            parts[0],
        );
        if self.dirty {
            if let Some(img) = &self.decoded {
                let scale = self.zoom.scale((img.width(), img.height()), pixels);
                let (prepared, crop) = prepare_image(img, pixels, scale, &mut self.center);
                self.crop = crop;
                self.picture = Some(picker.new_resize_protocol(prepared));
            }
            self.dirty = false;
        }
        if let Some(picture) = &mut self.picture {
            let size = picture.size_for(Resize::Crop(None), parts[1].as_size());
            let rect = Rect::new(
                parts[1].x + parts[1].width.saturating_sub(size.width) / 2,
                parts[1].y + parts[1].height.saturating_sub(size.height) / 2,
                size.width,
                size.height,
            );
            f.render_stateful_widget(
                StatefulImage::default().resize(Resize::Crop(None)),
                rect,
                picture,
            );
        } else {
            f.render_widget(
                Paragraph::new(self.message.as_str())
                    .wrap(Wrap { trim: false })
                    .style(Style::default().fg(colors.muted)),
                parts[1],
            );
        }
        let caption = self
            .entries
            .get(self.selected)
            .map(|e| {
                if e.caption.is_empty() {
                    e.url.as_str()
                } else {
                    e.caption.as_str()
                }
            })
            .unwrap_or("");
        f.render_widget(
            Paragraph::new(crate::clean(caption))
                .wrap(Wrap { trim: false })
                .style(Style::default().fg(colors.muted)),
            parts[2],
        );
        f.render_widget(Paragraph::new("[ / ] images · +/- zoom · 0 fit · 1 actual pixels\nArrows/HJKL pan · O open · R retry · Esc back").style(Style::default().fg(colors.accent)),parts[3]);
    }
}

// Resize exactly once from the original crop. The protocol then only pads to
// cell boundaries; it must not stretch pixels to fill those cells again.
fn prepare_image(
    img: &DynamicImage,
    viewport: (u32, u32),
    scale: f64,
    center: &mut (f64, f64),
) -> (DynamicImage, (u32, u32)) {
    let (x, y, w, h) = crop_rect((img.width(), img.height()), viewport, scale, center);
    let crop = img.crop_imm(x, y, w, h);
    let width = ((w as f64 * scale).round() as u32).clamp(1, viewport.0.max(1));
    let height = ((h as f64 * scale).round() as u32).clamp(1, viewport.1.max(1));
    let prepared = if (width, height) == (w, h) {
        crop
    } else {
        crop.resize_exact(width, height, image::imageops::FilterType::Lanczos3)
    };
    (prepared, (w, h))
}

fn crop_rect(
    image: (u32, u32),
    viewport: (u32, u32),
    scale: f64,
    center: &mut (f64, f64),
) -> (u32, u32, u32, u32) {
    let (iw, ih) = (image.0.max(1) as f64, image.1.max(1) as f64);
    let (vw, vh) = (viewport.0.max(1) as f64, viewport.1.max(1) as f64);
    let w = (vw / scale).round().clamp(1., iw) as u32;
    let h = (vh / scale).round().clamp(1., ih) as u32;
    center.0 = center.0.clamp(w as f64 / iw / 2., 1. - w as f64 / iw / 2.);
    center.1 = center.1.clamp(h as f64 / ih / 2., 1. - h as f64 / ih / 2.);
    let x = (center.0 * iw - w as f64 / 2.).round().max(0.) as u32;
    let y = (center.1 * ih - h as f64 / 2.).round().max(0.) as u32;
    (
        x.min(image.0.saturating_sub(w)),
        y.min(image.1.saturating_sub(h)),
        w,
        h,
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    #[ignore = "run by the isolated integration harness with cached fixture images"]
    fn gallery_and_refresh_round_trip() {
        use crate::{App, Article, Counts, Event, KeyModifiers};
        use crossterm::event::KeyEvent;
        use ratatui::{Terminal, backend::TestBackend};
        use std::time::Instant;
        let base = std::env::var("CURRENT_TEST_URL").expect("isolated server required");
        let id = std::env::var("CURRENT_TEST_GALLERY_ID")
            .unwrap()
            .parse::<i64>()
            .unwrap();
        let files: Vec<String> =
            serde_json::from_str(&std::env::var("CURRENT_TEST_GALLERY_CACHE").unwrap()).unwrap();
        for (i, file) in files.iter().enumerate() {
            let img = image::ImageBuffer::from_fn(800, 400, |x, y| {
                image::Rgb([((x + i as u32 * 50) % 256) as u8, (y % 256) as u8, 100])
            });
            std::fs::create_dir_all(std::path::Path::new(file).parent().unwrap()).unwrap();
            img.save_with_format(file, image::ImageFormat::Png).unwrap();
        }
        let a: Article = get(&base, &format!("articles/{id}")).unwrap();
        let mut app = App::from_library(
            base,
            Picker::halfblocks(),
            vec![],
            vec![],
            Counts::default(),
        );
        app.items = vec![a.clone()];
        app.article = Some(a);
        app.focus = 2;
        let press = |app: &mut App, code| {
            app.event(Event::Key(KeyEvent::new(code, KeyModifiers::NONE)));
        };
        press(&mut app, KeyCode::Char('v'));
        let wait_image = |app: &mut App| {
            let deadline = Instant::now() + Duration::from_secs(8);
            loop {
                app.messages();
                if app.gallery.as_ref().unwrap().decoded.is_some() {
                    break;
                }
                assert!(
                    Instant::now() < deadline,
                    "gallery failed: {}",
                    app.gallery.as_ref().unwrap().message
                );
                thread::sleep(Duration::from_millis(10));
            }
        };
        wait_image(&mut app);
        assert_eq!(app.gallery.as_ref().unwrap().entries.len(), 2);
        for (w, h) in [(65, 18), (100, 35), (170, 45)] {
            let mut terminal = Terminal::new(TestBackend::new(w, h)).unwrap();
            terminal.draw(|f| app.draw(f)).unwrap();
            assert!(!app.reader_visible);
            let screen: String = terminal
                .backend()
                .buffer()
                .content()
                .iter()
                .map(|c| c.symbol())
                .collect();
            assert!(screen.contains("800 × 400 px"));
            assert!(screen.contains("1 actual pixels"));
            press(&mut app, KeyCode::Char('1'));
            terminal.draw(|f| app.draw(f)).unwrap();
            assert_eq!(app.gallery.as_ref().unwrap().zoom, Zoom::Actual);
            let screen: String = terminal
                .backend()
                .buffer()
                .content()
                .iter()
                .map(|c| c.symbol())
                .collect();
            assert!(screen.contains("100% · actual pixels"));
            press(&mut app, KeyCode::Char('0'));
            terminal.draw(|f| app.draw(f)).unwrap();
            assert_eq!(app.gallery.as_ref().unwrap().zoom, Zoom::Fit);
        }
        press(&mut app, KeyCode::Char('+'));
        press(&mut app, KeyCode::Right);
        assert!(matches!(
            app.gallery.as_ref().unwrap().zoom,
            Zoom::Manual(_)
        ));
        press(&mut app, KeyCode::Char(']'));
        wait_image(&mut app);
        assert_eq!(app.gallery.as_ref().unwrap().selected, 1);
        assert_eq!(
            app.gallery.as_ref().unwrap().entries[1].caption,
            "Second gallery image"
        );
        press(&mut app, KeyCode::Esc);
        assert!(app.gallery.is_none());
        assert_eq!(app.article.as_ref().unwrap().id, id);
        let old = app.detail_generation;
        press(&mut app, KeyCode::Char('r'));
        assert!(app.refresh.requesting);
        let deadline = Instant::now() + Duration::from_secs(45);
        while app.refresh.finished_id == 0 || app.refresh.busy() || app.items.len() < 2 {
            app.messages();
            assert!(
                Instant::now() < deadline,
                "refresh did not settle: {} / {}",
                app.refresh.label(),
                app.message
            );
            thread::sleep(Duration::from_millis(20));
        }
        assert_eq!(app.refresh.progress.completed, app.refresh.progress.total);
        assert_eq!(app.article.as_ref().unwrap().id, id);
        assert_eq!(app.items[app.selected].id, id);
        assert_eq!(
            app.detail_generation, old,
            "refresh must keep the loaded reader"
        );
    }
    #[test]
    fn zoom_and_pan_stay_within_the_image_and_reset_to_fit() {
        let mut center = (0.5, 0.5);
        assert_eq!(
            crop_rect((1200, 600), (800, 600), 2. / 3., &mut center),
            (0, 0, 1200, 600)
        );
        let (_, _, w, h) = crop_rect((1200, 600), (800, 600), 1.5, &mut center);
        assert!(w < 1200 && h < 600);
        center = (-9., 9.);
        let (x, y, w, h) = crop_rect((1200, 600), (800, 600), 1.5, &mut center);
        assert_eq!(x, 0);
        assert_eq!(y + h, 600);
        assert!(x + w <= 1200);
        assert_eq!(
            crop_rect((1200, 600), (800, 600), 2. / 3., &mut center),
            (0, 0, 1200, 600)
        );
    }
    #[test]
    fn fit_does_not_enlarge_small_images_and_native_preserves_pixels() {
        use image::{GenericImageView, ImageBuffer, Rgb};
        use ratatui_image::FontSize;
        // Odd dimensions deliberately do not align with the terminal grid.
        let img: DynamicImage = ImageBuffer::from_fn(301, 173, |x, y| {
            Rgb([(x % 256) as u8, (y % 256) as u8, ((x + y) % 256) as u8])
        })
        .into();
        let viewport = (800, 600);
        let scale = Zoom::Fit.scale(img.dimensions(), viewport);
        assert_eq!(scale, 1.);
        let (prepared, _) = prepare_image(&img, viewport, scale, &mut (0.5, 0.5));
        assert_eq!(prepared.dimensions(), img.dimensions());
        assert_eq!(prepared.as_bytes(), img.as_bytes());

        // Verify the actual protocol's padding step, not just the crop helper.
        let resize = Resize::Crop(None);
        let font = FontSize::new(13, 27);
        let cells = resize.size_for(&prepared, font, Size::new(80, 30));
        let padded = resize.resize(&prepared, font, cells, None);
        assert_eq!(padded.crop_imm(0, 0, 301, 173).to_rgb8(), img.to_rgb8());

        let viewport = (117, 81);
        let scale = Zoom::Actual.scale(img.dimensions(), viewport);
        let (native, _) = prepare_image(&img, viewport, scale, &mut (1., 1.));
        assert_eq!(native.dimensions(), viewport);
        assert_eq!(native.to_rgb8(), img.crop_imm(184, 92, 117, 81).to_rgb8());
    }

    #[test]
    fn fit_and_manual_zoom_use_source_pixel_percentages() {
        let dimensions = (1600, 800);
        let viewport = (800, 600);
        assert_eq!(Zoom::Fit.scale(dimensions, viewport), 0.5);
        assert_eq!(Zoom::Actual.scale(dimensions, viewport), 1.);
        assert_eq!(Zoom::Manual(1.5).scale(dimensions, viewport), 1.5);
        let img = DynamicImage::new_rgb8(dimensions.0, dimensions.1);
        let (fit, crop) = prepare_image(&img, viewport, 0.5, &mut (0.5, 0.5));
        assert_eq!((fit.width(), fit.height()), (800, 400));
        assert_eq!(crop, dimensions);
        let (zoomed, crop) = prepare_image(&img, viewport, 1.5, &mut (0.5, 0.5));
        assert!(crop.0 < dimensions.0 && crop.1 < dimensions.1);
        assert_eq!((zoomed.width(), zoomed.height()), viewport);
    }
    #[test]
    fn old_image_results_cannot_replace_the_selected_image() {
        let mut gallery = Gallery::new("http://127.0.0.1:0".into(), 1, "Article".into());
        gallery.revision = 4;
        gallery
            .tx
            .send(Reply::Image(3, Ok(DynamicImage::new_rgb8(20, 20))))
            .unwrap();
        gallery.tick();
        assert!(gallery.decoded.is_none());
        gallery
            .tx
            .send(Reply::Image(4, Ok(DynamicImage::new_rgb8(40, 20))))
            .unwrap();
        gallery.tick();
        assert_eq!(gallery.decoded.as_ref().unwrap().width(), 40);
    }
}
