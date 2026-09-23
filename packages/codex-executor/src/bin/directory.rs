use rustix::fs::{FileType, fstat};
#[path = "../directory.rs"]
mod directory;
use directory::observe;
#[path = "../workspace_path.rs"]
mod workspace_path;
use serde_json::json;
use std::{io, os::fd::OwnedFd, path::Path};
use workspace_path::{anchor, directory};

fn run() -> io::Result<serde_json::Value> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args.len() != 3 {
        return Err(io::ErrorKind::InvalidInput.into());
    }
    list(&args[0], &args[1], &args[2])
}

fn list(root: &str, relative: &str, limit: &str) -> io::Result<serde_json::Value> {
    let limit = limit
        .parse()
        .map_err(|_| io::Error::from(io::ErrorKind::InvalidInput))?;
    if !(1..=4096).contains(&limit) {
        return Err(io::ErrorKind::InvalidInput.into());
    }
    // Validate every component first, so that a missing earlier component can
    // never hide an invalid request as a listable-directory result.
    if !relative.is_empty()
        && relative.split('/').any(|part| {
            part.is_empty()
                || part == "."
                || part == ".."
                || part.contains(['\\', '\0', '\r', '\n'])
        })
    {
        return Err(io::ErrorKind::InvalidInput.into());
    }
    let root = anchor(Path::new(root))?;
    let Some(selected) = select(&root, relative)? else {
        return Ok(json!({"version": 1, "error": "not_directory"}));
    };
    let result = observe(&selected, limit)?;
    let entries: Vec<_> = result
        .entries
        .into_iter()
        .map(|entry| {
            let kind = match entry.kind {
                FileType::RegularFile => "file",
                FileType::Directory => "directory",
                FileType::Symlink => "symlink",
                _ => "other",
            };
            json!({"name": entry.name, "kind": kind, "size_bytes": entry.size})
        })
        .collect();
    Ok(json!({"version": 1, "directory": {"entries": entries, "truncated": result.truncated}}))
}

/// Resolves the requested path below the anchored root. `None` means that
/// path does not name a listable directory: a missing component, a regular file
/// or a symbolic link (never followed). Root, permission and observation
/// failures keep their existing codes.
fn select(root: &OwnedFd, relative: &str) -> io::Result<Option<OwnedFd>> {
    match directory(root, relative) {
        Ok(selected) => Ok(Some(selected)),
        Err(error)
            if matches!(
                error.kind(),
                io::ErrorKind::NotFound | io::ErrorKind::NotADirectory
            ) =>
        {
            // A root removed after it was opened fails every lookup; that is
            // a missing workspace, not a missing requested directory.
            if fstat(root)?.st_nlink == 0 {
                return Err(io::ErrorKind::NotFound.into());
            }
            Ok(None)
        }
        Err(error) => Err(error),
    }
}

fn main() -> std::process::ExitCode {
    let response = match run() {
        Ok(value) => value,
        Err(error) => json!({"version": 1, "error": match error.kind() {
            io::ErrorKind::NotFound => "not_found",
            io::ErrorKind::PermissionDenied => "permission_denied",
            io::ErrorKind::InvalidInput | io::ErrorKind::NotADirectory => "invalid_path",
            _ => "native_error",
        }}),
    };
    let bytes = match serde_json::to_vec(&response) {
        Ok(bytes) if bytes.len() <= 4 * 1024 * 1024 => bytes,
        _ => b"{\"version\":1,\"error\":\"too_large\"}".to_vec(),
    };
    use io::Write;
    match io::stdout().lock().write_all(&bytes) {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(_) => std::process::ExitCode::FAILURE,
    }
}

#[cfg(test)]
mod tests {
    use super::{list, select};
    use crate::workspace_path::anchor;
    use std::{fs, io, os::unix::fs::symlink};

    fn code(result: io::Result<serde_json::Value>) -> String {
        match result {
            Ok(value) => value["error"].as_str().unwrap_or("listed").to_string(),
            Err(error) => format!("{:?}", error.kind()),
        }
    }

    #[test]
    fn only_the_requested_path_resolution_is_not_a_directory() {
        let workspace = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let root = workspace.path().join("workspace");
        fs::create_dir_all(root.join("d")).unwrap();
        fs::write(root.join("d/f.txt"), b"hidden").unwrap();
        fs::write(root.join("file.txt"), b"x").unwrap();
        fs::write(outside.path().join("secret"), b"secret").unwrap();
        symlink(root.join("d"), root.join("linkdir")).unwrap();
        symlink("d", root.join("relative-link")).unwrap();
        symlink(outside.path(), root.join("outside-link")).unwrap();
        symlink("missing", root.join("dangling")).unwrap();
        let root = root.to_str().unwrap();
        for path in [
            "missing",
            "missing/deeper",
            "file.txt",
            "file.txt/deeper",
            "linkdir",
            "linkdir/f.txt",
            "relative-link",
            "outside-link",
            "dangling",
        ] {
            let value = list(root, path, "10").unwrap();
            assert_eq!(
                value,
                serde_json::json!({"version": 1, "error": "not_directory"}),
                "{path}"
            );
        }
        let listed = list(root, "d", "10").unwrap();
        assert_eq!(listed["directory"]["entries"][0]["name"], "f.txt");
        // Invalid requests are rejected before any component is opened.
        for path in ["missing/..", "missing//x", "./d", "d/", "missing/a\\b"] {
            assert_eq!(code(list(root, path, "10")), "InvalidInput", "{path}");
        }
        // Root failures keep their existing classification.
        let missing_root = format!("{root}/missing");
        assert_eq!(code(list(&missing_root, "", "10")), "NotFound");
        let linked_root = format!("{root}/linkdir");
        assert_eq!(code(list(&linked_root, "", "10")), "NotADirectory");
        let file_root = format!("{root}/file.txt");
        assert_eq!(code(list(&file_root, "", "10")), "NotADirectory");
    }

    #[test]
    fn a_root_removed_after_opening_is_not_an_empty_directory() {
        let workspace = tempfile::tempdir().unwrap();
        let root = workspace.path().join("workspace");
        fs::create_dir_all(root.join("d")).unwrap();
        let anchored = anchor(&root).unwrap();
        assert!(matches!(select(&anchored, "missing"), Ok(None)));
        fs::remove_dir_all(&root).unwrap();
        for path in ["d", "missing", "missing/deeper"] {
            let error = select(&anchored, path).expect_err(path);
            assert_eq!(error.kind(), io::ErrorKind::NotFound, "{path}");
        }
        assert!(!matches!(select(&anchored, ""), Ok(None)));
    }
}
