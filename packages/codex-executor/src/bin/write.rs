#[path = "../workspace_path.rs"]
mod workspace_path;
#[path = "../write_file.rs"]
mod write_file;

use serde_json::json;
use std::io::{self, Write};
use std::path::Path;
use write_file::{InstallMode, Rejection};

/// The four-argument form keeps the replace mode of trusted initialization,
/// including Core-issued initial-file installs on older Runtime images. The
/// public Files.create writer appends `create`.
fn mode(args: &[String]) -> Option<InstallMode> {
    match args {
        [_, _, _, _] => Some(InstallMode::Replace),
        [_, _, _, _, mode] if mode == "create" => Some(InstallMode::Create),
        _ => None,
    }
}

fn run() -> Result<u64, write_file::Failure> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    let mode = mode(&args).ok_or_else(|| io::Error::from(io::ErrorKind::InvalidInput))?;
    let size = args[2]
        .parse::<u64>()
        .map_err(|_| io::Error::from(io::ErrorKind::InvalidInput))?;
    write_file::install(
        Path::new(&args[0]),
        &args[1],
        size,
        io::stdin().lock(),
        Path::new(&args[3]),
        mode,
    )?;
    Ok(size)
}

fn error_code(failure: &write_file::Failure) -> &'static str {
    match failure.rejection {
        Some(Rejection::Directory) => "destination_directory",
        Some(Rejection::Unsafe) => "unsafe_destination",
        None if failure.error.kind() == io::ErrorKind::InvalidInput => "invalid_input",
        None => "write_failed",
    }
}

fn main() -> std::process::ExitCode {
    let response = match run() {
        Ok(size) => json!({"version": 1, "outcome": "completed", "size_bytes": size}),
        Err(failure) => {
            json!({"version": 1, "outcome": if failure.committed { "unknown" } else { "failed" }, "error": error_code(&failure)})
        }
    };
    if writeln!(io::stdout().lock(), "{response}").is_err() {
        return std::process::ExitCode::FAILURE;
    }
    std::process::ExitCode::SUCCESS
}

#[cfg(test)]
mod cli_tests {
    use super::*;

    #[test]
    fn selects_mode_explicitly_and_reports_fixed_codes() {
        let args = |values: &[&str]| values.iter().map(|v| v.to_string()).collect::<Vec<_>>();
        assert_eq!(
            mode(&args(&["r", "p", "0", "s"])),
            Some(InstallMode::Replace)
        );
        assert_eq!(
            mode(&args(&["r", "p", "0", "s", "create"])),
            Some(InstallMode::Create)
        );
        for invalid in [
            args(&["r", "p", "0"]),
            args(&["r", "p", "0", "s", "replace"]),
            args(&["r", "p", "0", "s", "create", "x"]),
        ] {
            assert_eq!(mode(&invalid), None);
        }
        let failure = |rejection, kind| write_file::Failure {
            committed: false,
            rejection,
            error: io::Error::from(kind),
        };
        for (expected, value) in [
            (
                "destination_directory",
                failure(Some(Rejection::Directory), io::ErrorKind::AlreadyExists),
            ),
            (
                "unsafe_destination",
                failure(Some(Rejection::Unsafe), io::ErrorKind::AlreadyExists),
            ),
            ("invalid_input", failure(None, io::ErrorKind::InvalidInput)),
            ("write_failed", failure(None, io::ErrorKind::NotFound)),
        ] {
            assert_eq!(error_code(&value), expected);
        }
    }
}
