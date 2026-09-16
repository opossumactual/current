use crate::{clean, layout};
use ratatui::{buffer::CellDiffOption, prelude::*};
use std::{fmt::Write, num::NonZeroU16};

/// Reserve the entire multicell footprint in Ratatui, including the lower
/// halves of the glyphs. A normal widget replacing it will clear skipped cells.
/// No user-supplied escape codes are passed to the terminal.
pub struct Headline<'a> {
    pub lines: &'a [String],
    pub style: Style,
}
impl Widget for Headline<'_> {
    fn render(self, area: Rect, buf: &mut Buffer) {
        if area.width < 2 || area.height < 2 {
            return;
        }
        let mut sequence = String::new();
        for y in area.y..area.bottom() {
            write!(
                sequence,
                "\x1b[{};{}H\x1b[{}X",
                y + 1,
                area.x + 1,
                area.width
            )
            .unwrap();
            for x in area.x..area.right() {
                buf[(x, y)]
                    .set_style(self.style)
                    .set_diff_option(CellDiffOption::Skip);
            }
        }
        for (i, text) in self.lines.iter().take(area.height as usize / 2).enumerate() {
            let text = clean(text).split_whitespace().collect::<Vec<_>>().join(" ");
            let text = layout::ellipsis(&text, area.width as usize / 2);
            write!(
                sequence,
                "\x1b[{};{}H\x1b]66;s=2;{}\x1b\\",
                area.y + 1 + i as u16 * 2,
                area.x + 1,
                text
            )
            .unwrap();
        }
        // The backend believes the anchor occupies one cell. Restore that
        // exact cursor position before it writes the next widget.
        write!(sequence, "\x1b[{};{}H", area.y + 1, area.x + 2).unwrap();
        buf[(area.x, area.y)]
            .set_symbol(&sequence)
            .set_style(self.style)
            .set_diff_option(CellDiffOption::ForcedWidth(NonZeroU16::new(1).unwrap()));
    }
}
