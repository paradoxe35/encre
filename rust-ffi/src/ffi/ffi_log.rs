//! Hands every diagnostic from this crate, transcribe-cpp and ggml to the host, so they
//! land in the application's log instead of a stderr nobody reads.

use std::ffi::CString;
use std::io;
use std::os::raw::{c_char, c_int};

use parking_lot::Mutex;
use tracing::{Level, Metadata};
use tracing_subscriber::filter::LevelFilter;
use tracing_subscriber::fmt::MakeWriter;
use tracing_subscriber::layer::SubscriberExt;
use tracing_subscriber::util::SubscriberInitExt;

#[repr(C)]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LogLevel {
    Debug = 0,
    Info = 1,
    Warn = 2,
    Error = 3,
}

/// Receives one message at a time, from any thread. The string is only valid during the call.
pub type LogCallback = extern "C" fn(level: c_int, message: *const c_char);

static CALLBACK: Mutex<Option<LogCallback>> = Mutex::new(None);

#[unsafe(no_mangle)]
pub extern "C" fn encre_log_set_callback(callback: LogCallback) {
    *CALLBACK.lock() = Some(callback);
}

/// Routes `tracing`, `log` and the native speech library into the callback. Safe to call
/// more than once; only the first installs.
pub fn install() {
    let installed = tracing_subscriber::registry()
        .with(LevelFilter::INFO)
        .with(layer())
        .try_init()
        .is_ok();
    if installed {
        transcribe_cpp::init_logging();
    }
}

/// The message and its fields on one line; the level travels separately.
fn layer<S>() -> impl tracing_subscriber::Layer<S>
where
    S: tracing::Subscriber + for<'a> tracing_subscriber::registry::LookupSpan<'a>,
{
    tracing_subscriber::fmt::layer()
        .with_writer(ToCallback)
        .with_ansi(false)
        .with_level(false)
        .with_target(false)
        .without_time()
}

fn emit(level: LogLevel, message: &str) {
    let callback = *CALLBACK.lock();
    match callback {
        Some(callback) => {
            if let Ok(text) = CString::new(message) {
                callback(level as c_int, text.as_ptr());
            }
        }
        None => eprintln!("{level:?}: {message}"),
    }
}

fn level_of(level: &Level) -> LogLevel {
    match *level {
        Level::ERROR => LogLevel::Error,
        Level::WARN => LogLevel::Warn,
        Level::INFO => LogLevel::Info,
        _ => LogLevel::Debug,
    }
}

struct ToCallback;

impl<'a> MakeWriter<'a> for ToCallback {
    type Writer = LineWriter;

    fn make_writer(&'a self) -> Self::Writer {
        LineWriter::new(LogLevel::Info)
    }

    fn make_writer_for(&'a self, meta: &Metadata<'_>) -> Self::Writer {
        LineWriter::new(level_of(meta.level()))
    }
}

/// One writer per event: the formatter writes the line, the drop delivers it.
pub struct LineWriter {
    level: LogLevel,
    line: Vec<u8>,
}

impl LineWriter {
    fn new(level: LogLevel) -> Self {
        Self {
            level,
            line: Vec::new(),
        }
    }
}

impl io::Write for LineWriter {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.line.extend_from_slice(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

impl Drop for LineWriter {
    fn drop(&mut self) {
        let text = String::from_utf8_lossy(&self.line);
        let text = text.trim_end();
        if !text.is_empty() {
            emit(self.level, text);
        }
    }
}

#[cfg(test)]
mod tests {
    use std::ffi::CStr;

    use super::*;

    static RECEIVED: Mutex<Vec<(c_int, String)>> = Mutex::new(Vec::new());
    /// The callback is process-wide, so tests using it run one at a time.
    static SERIAL: Mutex<()> = Mutex::new(());

    extern "C" fn capture(level: c_int, message: *const c_char) {
        let text = unsafe { CStr::from_ptr(message) }.to_string_lossy().into_owned();
        RECEIVED.lock().push((level, text));
    }

    fn capturing(run: impl FnOnce()) -> Vec<(c_int, String)> {
        let _serial = SERIAL.lock();
        RECEIVED.lock().clear();
        encre_log_set_callback(capture);
        let subscriber = tracing_subscriber::registry()
            .with(LevelFilter::INFO)
            .with(layer());
        tracing::subscriber::with_default(subscriber, run);
        *CALLBACK.lock() = None;
        RECEIVED.lock().clone()
    }

    #[test]
    fn events_reach_the_callback_with_their_level() {
        let received = capturing(|| {
            tracing::info!("loaded");
            tracing::warn!("slow");
            tracing::error!("broken");
        });
        assert_eq!(
            received,
            vec![
                (LogLevel::Info as c_int, "loaded".to_owned()),
                (LogLevel::Warn as c_int, "slow".to_owned()),
                (LogLevel::Error as c_int, "broken".to_owned()),
            ]
        );
    }

    #[test]
    fn fields_follow_the_message() {
        let received = capturing(|| tracing::info!(pieces = 3, model = "a.gguf", "transcribing"));
        assert_eq!(received[0].1, "transcribing pieces=3 model=\"a.gguf\"");
    }

    #[test]
    fn a_formatted_message_is_kept_whole() {
        let name = "mic";
        let received = capturing(|| tracing::warn!("device '{name}' unavailable"));
        assert_eq!(received[0].1, "device 'mic' unavailable");
    }

    #[test]
    fn debug_events_are_filtered_out() {
        let received = capturing(|| tracing::debug!("noise"));
        assert!(received.is_empty());
    }

    #[test]
    fn a_message_with_an_interior_nul_is_dropped_not_truncated() {
        let received = capturing(|| tracing::info!("bad\0byte"));
        assert!(received.is_empty());
    }

    #[test]
    fn without_a_callback_nothing_panics() {
        let _serial = SERIAL.lock();
        *CALLBACK.lock() = None;
        emit(LogLevel::Info, "to stderr");
    }
}
