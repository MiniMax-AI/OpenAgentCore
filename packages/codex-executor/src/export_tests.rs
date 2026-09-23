use super::*;
use rustix::fs::{inotify, mkfifoat};
use std::collections::BTreeMap;
use std::ffi::CStr;
use std::fs;
use std::mem::MaybeUninit;
use std::os::unix::{fs::symlink, net::UnixListener};

#[test]
fn captures_nested_binary_empty_and_long_names_without_other_workspace_files() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    let root = tempfile::tempdir().unwrap();
    fs::create_dir_all(root.path().join("outputs/nested")).unwrap();
    fs::write(root.path().join("private.txt"), "not an output").unwrap();
    fs::write(root.path().join("outputs/empty"), []).unwrap();
    let body: Vec<_> = (0..=255).cycle().take(131_079).collect();
    let name = format!("outputs/nested/{}", "x".repeat(200));
    fs::write(root.path().join(&name), &body).unwrap();
    let mut bytes = Vec::new();
    outputs(root.path(), &mut bytes).unwrap();
    let mut archive = tar::Archive::new(bytes.as_slice());
    let mut actual = BTreeMap::new();
    for entry in archive.entries().unwrap() {
        let mut entry = entry.unwrap();
        assert!(entry.header().entry_type().is_file());
        let name = entry.path().unwrap().to_str().unwrap().to_owned();
        let mut data = Vec::new();
        entry.read_to_end(&mut data).unwrap();
        actual.insert(name, data);
    }
    assert_eq!(
        actual,
        BTreeMap::from([("outputs/empty".into(), vec![]), (name, body)])
    );
}

#[test]
fn absent_outputs_is_an_empty_archive() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    let root = tempfile::tempdir().unwrap();
    let mut bytes = Vec::new();
    outputs(root.path(), &mut bytes).unwrap();
    assert_eq!(
        tar::Archive::new(bytes.as_slice())
            .entries()
            .unwrap()
            .count(),
        0
    );
}

/// Returns pending inotify events as (flags, entry name) without blocking.
fn drain(watcher: &OwnedFd) -> Vec<(inotify::ReadFlags, Option<String>)> {
    let mut buffer = [MaybeUninit::<u8>::uninit(); 8192];
    let mut reader = inotify::Reader::new(watcher, &mut buffer);
    let mut events = Vec::new();
    loop {
        match reader.next() {
            Ok(event) => events.push((
                event.events(),
                event.file_name().map(CStr::to_string_lossy).map(Into::into),
            )),
            Err(rustix::io::Errno::AGAIN) => return events,
            Err(error) => panic!("inotify read: {error}"),
        }
    }
}

#[test]
fn skips_symlinks_without_following_or_opening_their_targets() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    let root = tempfile::tempdir().unwrap();
    let other = tempfile::tempdir().unwrap();
    let secret = b"outside workspace secret marker";
    let private = b"workspace file outside outputs marker";
    fs::write(other.path().join("secret"), secret).unwrap();
    fs::create_dir(other.path().join("directory")).unwrap();
    fs::write(other.path().join("directory/secret"), secret).unwrap();
    fs::write(root.path().join("private.txt"), private).unwrap();
    fs::create_dir_all(root.path().join("outputs/sub")).unwrap();
    fs::write(root.path().join("outputs/a.txt"), "alpha").unwrap();
    fs::write(root.path().join("outputs/sub/b.txt"), "bravo").unwrap();
    fs::write(root.path().join("outputs/empty.txt"), []).unwrap();
    let tree = root.path().join("outputs");
    for (link, target) in [
        ("link.txt", Path::new("a.txt").to_path_buf()),
        ("sub-link", "sub".into()),
        ("sub/parent", "..".into()),
        ("self", "self".into()),
        ("dangling", "missing".into()),
        ("private-link", "../private.txt".into()),
        ("outside-file", other.path().join("secret")),
        ("outside-directory", other.path().join("directory")),
        ("sub/outside-root", other.path().into()),
    ] {
        symlink(target, tree.join(link)).unwrap();
    }
    // Any open or read of a target through a followed link would be observed here.
    let watcher =
        inotify::init(inotify::CreateFlags::NONBLOCK | inotify::CreateFlags::CLOEXEC).unwrap();
    let flags = inotify::WatchFlags::OPEN | inotify::WatchFlags::ACCESS;
    for watched in [
        other.path().to_path_buf(),
        other.path().join("directory"),
        root.path().join("private.txt"),
    ] {
        inotify::add_watch(&watcher, &watched, flags).unwrap();
    }
    let mut bytes = Vec::new();
    outputs(root.path(), &mut bytes).unwrap();
    assert_eq!(drain(&watcher), [], "a link target was opened or read");
    let mut archive = tar::Archive::new(bytes.as_slice());
    let mut actual = BTreeMap::new();
    for entry in archive.entries().unwrap() {
        let mut entry = entry.unwrap();
        assert!(entry.header().entry_type().is_file());
        let name = entry.path().unwrap().to_str().unwrap().to_owned();
        let mut data = Vec::new();
        entry.read_to_end(&mut data).unwrap();
        actual.insert(name, data);
    }
    assert_eq!(
        actual,
        BTreeMap::from([
            ("outputs/a.txt".into(), b"alpha".to_vec()),
            ("outputs/empty.txt".into(), vec![]),
            ("outputs/sub/b.txt".into(), b"bravo".to_vec()),
        ])
    );
    for marker in [&secret[..], &private[..]] {
        assert!(!bytes.windows(marker.len()).any(|part| part == marker));
    }
    // Positive control: the watcher does report an actual target read.
    assert_eq!(fs::read(other.path().join("secret")).unwrap(), secret);
    assert!(
        drain(&watcher)
            .iter()
            .any(|(flags, name)| flags.contains(inotify::ReadFlags::OPEN)
                && name.as_deref() == Some("secret")),
        "inotify control was not observed"
    );
}

#[test]
fn rejects_root_link_hard_links_and_special_files_without_exposing_their_contents() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    for kind in ["root-symlink", "hardlink", "socket", "fifo"] {
        let root = tempfile::tempdir().unwrap();
        let other = tempfile::tempdir().unwrap();
        let secret = b"private daemon credential marker";
        fs::write(other.path().join("secret"), secret).unwrap();
        if kind == "root-symlink" {
            symlink(other.path(), root.path().join("outputs")).unwrap();
        } else {
            fs::create_dir(root.path().join("outputs")).unwrap();
            fs::write(root.path().join("outputs/regular"), "kept only on success").unwrap();
        }
        let target = root.path().join("outputs/file");
        let _socket = match kind {
            "hardlink" => {
                fs::hard_link(other.path().join("secret"), target).unwrap();
                None
            }
            "socket" => Some(UnixListener::bind(target).unwrap()),
            "fifo" => {
                mkfifoat(rustix::fs::CWD, &target, Mode::RUSR | Mode::WUSR).unwrap();
                None
            }
            _ => None,
        };
        let mut bytes = Vec::new();
        assert!(outputs(root.path(), &mut bytes).is_err(), "{kind}");
        assert!(
            !bytes.windows(secret.len()).any(|part| part == secret),
            "{kind}"
        );
    }
}

#[test]
fn enforces_file_and_total_bytes_without_loading_bodies() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    for sizes in [
        vec![MAX_FILE_BYTES + 1],
        vec![MAX_FILE_BYTES, MAX_FILE_BYTES, 101 << 20],
    ] {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("outputs")).unwrap();
        for (index, size) in sizes.iter().enumerate() {
            File::create(root.path().join(format!("outputs/{index}")))
                .unwrap()
                .set_len(*size)
                .unwrap();
        }
        assert!(outputs(root.path(), io::sink()).is_err());
    }
}

#[test]
fn rejects_a_changed_file_or_directory_during_export() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    struct Mutate<'a> {
        root: &'a Path,
        change: &'static str,
        done: bool,
    }
    impl Write for Mutate<'_> {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            if !self.done {
                self.done = true;
                match self.change {
                    "file" => fs::write(self.root.join("outputs/file"), "changed-length")?,
                    "directory" => fs::write(self.root.join("outputs/new"), "late")?,
                    // Skipped links still take part in the directory listing check.
                    _ => symlink("file", self.root.join("outputs/late-link"))?,
                }
            }
            Ok(bytes.len())
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
    for change in ["file", "directory", "link"] {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("outputs")).unwrap();
        fs::write(root.path().join("outputs/file"), "initial").unwrap();
        assert!(
            outputs(
                root.path(),
                Mutate {
                    root: root.path(),
                    change,
                    done: false
                }
            )
            .is_err(),
            "change={change}"
        );
    }
}

#[test]
fn rejects_truncated_traversal_and_broken_destination() {
    let _guard = crate::directory::TEST_LOCK.lock().unwrap();
    let root = tempfile::tempdir().unwrap();
    fs::create_dir(root.path().join("outputs")).unwrap();
    for index in 0..=MAX_ENTRIES {
        fs::write(root.path().join(format!("outputs/{index}")), []).unwrap();
    }
    assert!(outputs(root.path(), io::sink()).is_err());
    struct Broken;
    impl Write for Broken {
        fn write(&mut self, _: &[u8]) -> io::Result<usize> {
            Err(io::ErrorKind::BrokenPipe.into())
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
    let empty = tempfile::tempdir().unwrap();
    assert!(outputs(empty.path(), Broken).is_err());
}
