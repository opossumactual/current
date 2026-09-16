use serde::Deserialize;
use std::time::{Duration, Instant};

#[derive(Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Progress {
    pub id: u64,
    pub running: bool,
    pub total: usize,
    pub completed: usize,
    pub inserted: usize,
    pub failed: usize,
}
pub struct State {
    pub progress: Progress,
    pub requesting: bool,
    pub polling: bool,
    pub last_poll: Instant,
    pub finished_id: u64,
    pub updated: Option<Instant>,
    pub error: bool,
}
impl Default for State {
    fn default() -> Self {
        Self {
            progress: Progress::default(),
            requesting: false,
            polling: false,
            last_poll: Instant::now(),
            finished_id: 0,
            updated: None,
            error: false,
        }
    }
}
impl State {
    pub fn busy(&self) -> bool {
        self.requesting || self.progress.running
    }
    pub fn accept(&mut self, p: Progress) -> bool {
        if p.id < self.progress.id
            || p.id == self.progress.id && p.completed < self.progress.completed
        {
            return false;
        }
        self.progress = p;
        self.error = false;
        if self.progress.id > self.finished_id && !self.progress.running {
            self.finished_id = self.progress.id;
            self.updated = Some(Instant::now());
            return true;
        }
        false
    }
    pub fn label(&self) -> String {
        if self.error {
            return "Refresh status unavailable · r retry".into();
        }
        if self.requesting {
            return "↻ Starting refresh…".into();
        }
        let p = &self.progress;
        if p.running {
            return format!("↻ {}/{} feeds · {} new", p.completed, p.total, p.inserted);
        }
        if let Some(since) = self.updated {
            if since.elapsed() < Duration::from_secs(15) {
                return format!("✓ {} new · {} failed", p.inserted, p.failed);
            }
            return format!("Updated {}m ago", since.elapsed().as_secs() / 60);
        }
        "r refresh".into()
    }
    pub fn escape(&self) -> String {
        if self.error {
            "\x1b]9;4;0;0\x1b\\".into()
        } else if self.requesting {
            "\x1b]9;4;3;0\x1b\\".into()
        } else if self.progress.running {
            format!(
                "\x1b]9;4;1;{}\x1b\\",
                self.progress.completed.saturating_mul(100) / self.progress.total.max(1)
            )
        } else {
            "\x1b]9;4;0;0\x1b\\".into()
        }
    }
}

/// Native window progress is optional; other terminals get the in-app status.
/// Clear it during both normal exit and unwinding.
pub struct WindowProgress {
    enabled: bool,
    previous: String,
}
impl WindowProgress {
    pub fn new() -> Self {
        Self {
            enabled: std::env::var("TERM").is_ok_and(|s| s == "xterm-kitty")
                && std::env::var_os("TMUX").is_none(),
            previous: String::new(),
        }
    }
    pub fn update(&mut self, state: &State) -> std::io::Result<()> {
        use std::io::Write;
        if !self.enabled {
            return Ok(());
        }
        let sequence = state.escape();
        if sequence != self.previous {
            let mut stdout = std::io::stdout().lock();
            stdout.write_all(sequence.as_bytes())?;
            stdout.flush()?;
            self.previous = sequence;
        }
        Ok(())
    }
}
impl Drop for WindowProgress {
    fn drop(&mut self) {
        use std::io::Write;
        if self.enabled {
            let mut stdout = std::io::stdout().lock();
            let _ = stdout.write_all(b"\x1b]9;4;0;0\x1b\\");
            let _ = stdout.flush();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn progress_is_monotonic_and_finishes_only_once() {
        let mut state = State::default();
        assert!(!state.accept(Progress {
            id: 1,
            running: true,
            total: 3,
            completed: 2,
            inserted: 4,
            ..Progress::default()
        }));
        assert!(state.label().contains("2/3 feeds"));
        assert!(!state.accept(Progress {
            id: 1,
            running: true,
            total: 3,
            completed: 1,
            ..Progress::default()
        }));
        assert_eq!(state.progress.completed, 2);
        let done = Progress {
            id: 1,
            running: false,
            total: 3,
            completed: 3,
            inserted: 4,
            failed: 1,
        };
        assert!(state.accept(done.clone()));
        assert!(!state.accept(done));
        assert!(state.label().contains("4 new · 1 failed"));
        assert!(state.escape().contains("9;4;0;0"));
    }
}
