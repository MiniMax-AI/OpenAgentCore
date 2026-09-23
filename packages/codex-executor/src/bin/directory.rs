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
    let root_path = Path::new(root);
    let root = anchor(root_path)?;
    list_anchored(root_path, &root, relative, limit)
}

fn list_anchored(
    root_path: &Path,
    root: &OwnedFd,
    relative: &str,
    limit: usize,
) -> io::Result<serde_json::Value> {
    let Some(selected) = select(root, relative)? else {
        root_unchanged(root_path, root)?;
        return Ok(json!({"version": 1, "error": "not_directory"}));
    };
    let result = observe(&selected, limit)?;
    // Reading a removed directory ends like an empty one, so an empty listing
    // also confirms that the workspace root is still in place.
    if result.entries.is_empty() {
        root_unchanged(root_path, root)?;
    }
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
            Ok(None)
        }
        Err(error) => Err(error),
    }
}

/// Confirms that the root path still names the held root directory. The path
/// is reopened without following links and compared by device and inode, which
/// does not depend on link counts that some filesystems (such as overlayfs)
/// keep for removed directories. A removed or replaced root is a missing
/// workspace, not a missing requested directory.
fn root_unchanged(root_path: &Path, root: &OwnedFd) -> io::Result<()> {
    let current = match anchor(root_path) {
        Ok(current) => current,
        Err(error)
            if matches!(
                error.kind(),
                io::ErrorKind::NotFound | io::ErrorKind::NotADirectory
            ) =>
        {
            return Err(io::ErrorKind::NotFound.into());
        }
        Err(error) => return Err(error),
    };
    let (held, now) = (fstat(root)?, fstat(&current)?);
    if held.st_dev != now.st_dev || held.st_ino != now.st_ino {
        return Err(io::ErrorKind::NotFound.into());
    }
    Ok(())
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
    use super::{list, list_anchored};
    use crate::directory::observe;
    use crate::workspace_path::{anchor, directory};
    use std::{fs, io, os::unix::fs::symlink};

    // These tests open descriptors, so they share the lock that keeps the
    // descriptor-count assertions in the directory tests deterministic.
    fn serialized() -> std::sync::MutexGuard<'static, ()> {
        crate::directory::TEST_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    fn code(result: io::Result<serde_json::Value>) -> String {
        match result {
            Ok(value) => value["error"].as_str().unwrap_or("listed").to_string(),
            Err(error) => format!("{:?}", error.kind()),
        }
    }

    #[test]
    fn only_the_requested_path_resolution_is_not_a_directory() {
        let _guard = serialized();
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

    fn not_found(result: io::Result<serde_json::Value>) -> bool {
        matches!(result, Err(error) if error.kind() == io::ErrorKind::NotFound)
    }

    #[test]
    fn a_root_removed_after_opening_is_a_missing_workspace() {
        let _guard = serialized();
        let workspace = tempfile::tempdir().unwrap();
        let root = workspace.path().join("workspace");
        fs::create_dir_all(root.join("d")).unwrap();
        let anchored = anchor(&root).unwrap();
        let listed = list_anchored(&root, &anchored, "missing", 10).unwrap();
        assert_eq!(listed["error"], "not_directory");
        let selected = directory(&anchored, "").unwrap();
        fs::remove_dir_all(&root).unwrap();
        // Reading the removed root ends like an empty directory.
        assert!(observe(&selected, 10).unwrap().entries.is_empty());
        for path in ["", "d", "missing", "missing/deeper"] {
            assert!(
                not_found(list_anchored(&root, &anchored, path, 10)),
                "{path}"
            );
        }
    }

    #[test]
    fn a_root_replaced_after_opening_is_a_missing_workspace() {
        let _guard = serialized();
        let workspace = tempfile::tempdir().unwrap();
        let root = workspace.path().join("workspace");
        let moved = workspace.path().join("moved");
        fs::create_dir_all(root.join("d")).unwrap();
        fs::create_dir_all(root.join("empty")).unwrap();
        fs::write(root.join("d/f.txt"), b"x").unwrap();
        let anchored = anchor(&root).unwrap();
        fs::rename(&root, &moved).unwrap();
        fs::create_dir_all(root.join("d")).unwrap();
        // The held root stays authoritative for entries it still lists.
        let listed = list_anchored(&root, &anchored, "d", 10).unwrap();
        assert_eq!(listed["directory"]["entries"][0]["name"], "f.txt");
        for path in ["missing", "empty"] {
            assert!(
                not_found(list_anchored(&root, &anchored, path, 10)),
                "{path}"
            );
        }
        fs::remove_dir_all(&root).unwrap();
        symlink(&moved, &root).unwrap();
        assert!(not_found(list_anchored(&root, &anchored, "missing", 10)));
        // An unchanged root still lists missing paths and empty directories.
        let anchored = anchor(&moved).unwrap();
        let listed = list_anchored(&moved, &anchored, "missing", 10).unwrap();
        assert_eq!(listed["error"], "not_directory");
        let listed = list_anchored(&moved, &anchored, "empty", 10).unwrap();
        assert_eq!(listed["directory"]["entries"], serde_json::json!([]));
    }
}
