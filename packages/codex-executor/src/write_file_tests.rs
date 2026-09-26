use super::*;
use std::fs;
use std::io::Cursor;
use std::os::unix::fs::{MetadataExt, PermissionsExt, symlink};

fn fixture() -> tempfile::TempDir {
    let root = std::path::PathBuf::from(std::env::var_os("HOME").expect("HOME required"))
        .join(".oac/tests/scoped-write");
    fs::create_dir_all(&root).unwrap();
    tempfile::tempdir_in(root).unwrap()
}

fn frame(data: &[u8]) -> impl Read + '_ {
    Cursor::new(data).chain(Cursor::new(Sha256::digest(data).to_vec()))
}

fn install(root: &Path, relative: &str, size: u64, input: impl Read) -> Result<(), Failure> {
    let staging = fixture();
    let result = super::install(
        root,
        relative,
        size,
        input,
        staging.path(),
        InstallMode::Replace,
    );
    assert_eq!(fs::read_dir(staging.path()).unwrap().count(), 0);
    // Replace mode keeps the legacy error codes of trusted initialization.
    assert!(result.as_ref().err().is_none_or(|f| f.rejection.is_none()));
    result
}

#[test]
fn requires_existing_disjoint_nofollow_staging_before_consuming_input() {
    let f = fixture();
    let root = f.path().join("workspace");
    let staging = f.path().join("private");
    fs::create_dir_all(root.join("nested")).unwrap();
    fs::create_dir(&staging).unwrap();
    fs::write(root.join("file"), b"old").unwrap();
    symlink(&staging, f.path().join("link")).unwrap();
    struct Unread;
    impl Read for Unread {
        fn read(&mut self, _: &mut [u8]) -> io::Result<usize> {
            panic!("invalid staging must be rejected before reading input")
        }
    }
    for invalid in [
        root.clone(),
        root.join("nested"),
        f.path().to_path_buf(),
        f.path().join("missing"),
        f.path().join("link"),
        staging.join("../private"),
        Path::new("relative").to_path_buf(),
    ] {
        for mode in [InstallMode::Replace, InstallMode::Create] {
            let failure = super::install(&root, "new/file", 3, Unread, &invalid, mode).unwrap_err();
            assert!(!failure.committed);
            assert!(failure.rejection.is_none());
        }
        let failure =
            super::install(&root, "file", 3, Unread, &invalid, InstallMode::Replace).unwrap_err();
        assert!(!failure.committed);
        assert_eq!(fs::read(root.join("file")).unwrap(), b"old");
        assert!(!root.join("new").exists());
    }
    assert_eq!(fs::read_dir(&staging).unwrap().count(), 0);
}

#[test]
fn replaces_only_destination_hard_link_and_keeps_exact_binary_bytes() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir(&root).unwrap();
    let outside = f.path().join("outside");
    fs::write(&outside, b"outside").unwrap();
    fs::hard_link(&outside, root.join("file")).unwrap();
    let input: Vec<_> = (0..=255).cycle().take(256 * 1024).collect();
    install(&root, "file", input.len() as u64, frame(&input)).unwrap();
    assert_eq!(fs::read(&outside).unwrap(), b"outside");
    assert_eq!(fs::read(root.join("file")).unwrap(), input);
    assert_ne!(
        fs::metadata(&outside).unwrap().ino(),
        fs::metadata(root.join("file")).unwrap().ino()
    );
    assert_eq!(
        fs::metadata(root.join("file"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    install(&root, "file", 0, frame(b"")).unwrap();
    assert!(fs::read(root.join("file")).unwrap().is_empty());
}

#[test]
fn incomplete_excess_and_failed_input_preserve_old_file_and_remove_staging() {
    let f = fixture();
    fs::write(f.path().join("file"), b"old").unwrap();
    for (size, data) in [(4, b"new".as_slice()), (2, b"new".as_slice())] {
        assert!(
            !install(f.path(), "file", size, Cursor::new(data))
                .unwrap_err()
                .committed
        );
        assert_eq!(fs::read(f.path().join("file")).unwrap(), b"old");
        assert_eq!(fs::read_dir(f.path()).unwrap().count(), 1);
    }
    struct Failing;
    impl Read for Failing {
        fn read(&mut self, _: &mut [u8]) -> io::Result<usize> {
            Err(io::ErrorKind::BrokenPipe.into())
        }
    }
    assert!(!install(f.path(), "file", 3, Failing).unwrap_err().committed);
    assert_eq!(fs::read(f.path().join("file")).unwrap(), b"old");
    assert_eq!(fs::read_dir(f.path()).unwrap().count(), 1);
}

#[test]
fn rejects_symlinks_traversal_nonregular_targets_and_oversized_inputs() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir(&root).unwrap();
    fs::create_dir(f.path().join("outside")).unwrap();
    fs::write(f.path().join("outside/file"), b"canary").unwrap();
    symlink(f.path().join("outside/file"), root.join("link")).unwrap();
    symlink(f.path().join("outside"), root.join("parent")).unwrap();
    for name in [
        "link",
        "parent/file",
        "../outside/file",
        "/file",
        "a/../file",
        "a//file",
        "",
        ".",
        "a\\b",
        "x\n",
        "missing/file",
    ] {
        assert!(
            install(&root, name, 3, Cursor::new(b"new")).is_err(),
            "{name:?}"
        );
    }
    assert!(install(&root, "parent", 3, Cursor::new(b"new")).is_err());
    fs::create_dir(root.join("directory")).unwrap();
    assert!(install(&root, "directory", 0, io::empty()).is_err());
    assert!(install(&root, "large", MAX_BYTES + 1, io::empty()).is_err());
    assert_eq!(fs::read(f.path().join("outside/file")).unwrap(), b"canary");
}

#[test]
fn supports_large_stream_without_whole_input_allocation() {
    let f = fixture();
    let mut digest = Sha256::new();
    for _ in 0..(MAX_BYTES / 8192) {
        digest.update([0x91; 8192]);
    }
    let input = io::repeat(0x91)
        .take(MAX_BYTES)
        .chain(Cursor::new(digest.finalize().to_vec()));
    install(f.path(), "large", MAX_BYTES, input).unwrap();
    let file = fs::File::open(f.path().join("large")).unwrap();
    assert_eq!(file.metadata().unwrap().len(), MAX_BYTES);
    let mut content = io::BufReader::new(file);
    let mut chunk = [0u8; 8192];
    loop {
        let n = content.read(&mut chunk).unwrap();
        if n == 0 {
            break;
        }
        assert!(chunk[..n].iter().all(|x| *x == 0x91));
    }
}

#[test]
fn ancestor_replacement_during_input_cannot_redirect_commit() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir_all(root.join("a")).unwrap();
    fs::create_dir(f.path().join("outside")).unwrap();
    fs::write(f.path().join("outside/file"), b"outside").unwrap();
    let mut changed = false;
    let mut bytes = Cursor::new(b"inside");
    let input = std::io::Read::by_ref(&mut bytes);
    struct Replace<'a> {
        read: &'a mut Cursor<&'static [u8; 6]>,
        change: Box<dyn FnMut() + 'a>,
    }
    impl Read for Replace<'_> {
        fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
            (self.change)();
            self.read.read(buf)
        }
    }
    let input = Replace {
        read: input,
        change: Box::new(|| {
            if !changed {
                fs::rename(root.join("a"), root.join("held")).unwrap();
                symlink(f.path().join("outside"), root.join("a")).unwrap();
                changed = true;
            }
        }),
    };
    install(
        &root,
        "a/file",
        6,
        input.chain(Cursor::new(Sha256::digest(b"inside").to_vec())),
    )
    .unwrap();
    assert_eq!(fs::read(root.join("held/file")).unwrap(), b"inside");
    assert_eq!(fs::read(f.path().join("outside/file")).unwrap(), b"outside");
}

#[test]
fn requires_matching_commit_trailer_and_does_not_wait_for_eof() {
    let f = fixture();
    fs::write(f.path().join("file"), b"old").unwrap();
    for data in [b"new".to_vec(), [b"new".as_slice(), &[0u8; 32]].concat()] {
        assert!(install(f.path(), "file", 3, Cursor::new(data)).is_err());
        assert_eq!(fs::read(f.path().join("file")).unwrap(), b"old");
    }
    struct NoMore;
    impl Read for NoMore {
        fn read(&mut self, _: &mut [u8]) -> io::Result<usize> {
            panic!("must stop after commit trailer")
        }
    }
    install(f.path(), "file", 3, frame(b"new").chain(NoMore)).unwrap();
    assert_eq!(fs::read(f.path().join("file")).unwrap(), b"new");
}

fn create(root: &Path, relative: &str, size: u64, input: impl Read) -> Result<(), Failure> {
    let staging = fixture();
    let result = super::install(
        root,
        relative,
        size,
        input,
        staging.path(),
        InstallMode::Create,
    );
    assert_eq!(fs::read_dir(staging.path()).unwrap().count(), 0);
    result
}

fn rejection(result: Result<(), Failure>) -> Option<Rejection> {
    let failure = result.unwrap_err();
    assert!(!failure.committed);
    failure.rejection
}

fn mode(path: &Path) -> u32 {
    fs::symlink_metadata(path).unwrap().permissions().mode() & 0o777
}

struct Unread;

impl Read for Unread {
    fn read(&mut self, _: &mut [u8]) -> io::Result<usize> {
        panic!("a known conflict must be rejected before reading input")
    }
}

#[test]
fn create_mode_makes_missing_parents_after_verified_input() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir(&root).unwrap();
    create(&root, "top.txt", 3, frame(b"top")).unwrap();
    create(&root, "n1/n2/nested.txt", 6, frame(b"nested")).unwrap();
    assert_eq!(fs::read(root.join("n1/n2/nested.txt")).unwrap(), b"nested");
    assert_eq!(fs::read(root.join("top.txt")).unwrap(), b"top");
    for created in ["n1", "n1/n2"] {
        assert!(fs::symlink_metadata(root.join(created)).unwrap().is_dir());
        assert_eq!(mode(&root.join(created)), 0o700);
    }
    assert_eq!(mode(&root.join("n1/n2/nested.txt")), 0o600);
    // An existing prefix is reused; only the missing tail is created.
    create(&root, "n1/n3/deeper/empty", 0, frame(b"")).unwrap();
    assert!(
        fs::read(root.join("n1/n3/deeper/empty"))
            .unwrap()
            .is_empty()
    );
    // Incomplete or mismatched input creates no parents.
    assert!(rejection(create(&root, "p1/p2/file", 3, Cursor::new(b"ab"))).is_none());
    let mismatched = [b"abc".as_slice(), &[0u8; 32]].concat();
    assert!(rejection(create(&root, "p1/p2/file", 3, Cursor::new(mismatched))).is_none());
    assert!(!root.join("p1").exists());
}

#[test]
fn create_mode_never_replaces_existing_destinations() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir_all(root.join("n1/n2")).unwrap();
    let outside = f.path().join("outside");
    fs::write(&outside, b"outside").unwrap();
    fs::hard_link(&outside, root.join("alias")).unwrap();
    fs::write(root.join("n1/tracked.txt"), b"old").unwrap();
    fs::write(root.join("empty"), b"").unwrap();
    rustix::fs::mknodat(
        rustix::fs::CWD,
        root.join("fifo").as_path(),
        FileType::Fifo,
        rustix::fs::Mode::from_raw_mode(0o600),
        0,
    )
    .unwrap();
    for name in ["n1/tracked.txt", "alias", "empty", "fifo"] {
        assert_eq!(
            rejection(create(&root, name, 3, Unread)),
            Some(Rejection::Unsafe),
            "{name}"
        );
    }
    for name in ["n1", "n1/n2"] {
        assert_eq!(
            rejection(create(&root, name, 3, Unread)),
            Some(Rejection::Directory),
            "{name}"
        );
        assert!(fs::symlink_metadata(root.join(name)).unwrap().is_dir());
    }
    assert_eq!(fs::read(root.join("n1/tracked.txt")).unwrap(), b"old");
    assert_eq!(fs::read(&outside).unwrap(), b"outside");
    assert_eq!(
        fs::metadata(&outside).unwrap().ino(),
        fs::metadata(root.join("alias")).unwrap().ino()
    );
    assert!(fs::read(root.join("empty")).unwrap().is_empty());
    // A second create of the same path is refused after the first commits.
    create(&root, "n1/once.txt", 3, frame(b"one")).unwrap();
    assert_eq!(
        rejection(create(&root, "n1/once.txt", 3, frame(b"two"))),
        Some(Rejection::Unsafe)
    );
    assert_eq!(fs::read(root.join("n1/once.txt")).unwrap(), b"one");
}

#[test]
fn create_mode_rejects_symlink_and_nondirectory_components() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir_all(root.join("real")).unwrap();
    let outside = f.path().join("outside");
    fs::create_dir(&outside).unwrap();
    fs::write(outside.join("file"), b"canary").unwrap();
    symlink(outside.join("file"), root.join("leaf-link")).unwrap();
    symlink(&outside, root.join("dir-link")).unwrap();
    symlink(&outside, root.join("real/nested-link")).unwrap();
    symlink(root.join("real"), root.join("inside-link")).unwrap();
    symlink(f.path().join("missing"), root.join("dangling")).unwrap();
    fs::write(root.join("plain"), b"plain").unwrap();
    for name in [
        "leaf-link",
        "dir-link",
        "dangling",
        "inside-link",
        "dir-link/file",
        "dir-link/new/file",
        "real/nested-link/file",
        "inside-link/file",
        "dangling/file",
        "plain/child",
        "plain/child/deeper",
    ] {
        assert_eq!(
            rejection(create(&root, name, 3, Unread)),
            Some(Rejection::Unsafe),
            "{name}"
        );
    }
    assert_eq!(fs::read(outside.join("file")).unwrap(), b"canary");
    assert_eq!(fs::read_dir(&outside).unwrap().count(), 1);
    assert_eq!(fs::read_dir(root.join("real")).unwrap().count(), 1);
    assert!(!f.path().join("missing").exists());
    for invalid in [
        "../outside/file",
        "/file",
        "a/../b",
        "a//b",
        "a\\b",
        "x\n",
        ".",
    ] {
        assert!(
            rejection(create(&root, invalid, 3, Unread)).is_none(),
            "{invalid:?}"
        );
    }
}

/// Changes the workspace once, after the pre-install checks and before commit.
struct Race<'a, R> {
    inner: R,
    change: Option<Box<dyn FnOnce() + 'a>>,
}

impl<R: Read> Read for Race<'_, R> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if let Some(change) = self.change.take() {
            change();
        }
        self.inner.read(buf)
    }
}

fn racing<'a>(data: &'a [u8], change: impl FnOnce() + 'a) -> impl Read + 'a {
    Race {
        inner: frame(data),
        change: Some(Box::new(change)),
    }
}

#[test]
fn create_mode_race_never_replaces_or_follows() {
    let f = fixture();
    let root = f.path().join("workspace");
    fs::create_dir(&root).unwrap();
    let outside = f.path().join("outside");
    fs::create_dir(&outside).unwrap();
    // The destination appears after the leaf check.
    let target = root.join("raced.txt");
    let input = racing(b"api", || fs::write(&target, b"racer").unwrap());
    assert_eq!(
        rejection(create(&root, "raced.txt", 3, input)),
        Some(Rejection::Unsafe)
    );
    assert_eq!(fs::read(&target).unwrap(), b"racer");
    // A directory appears at the destination.
    let input = racing(b"api", || fs::create_dir(root.join("raced-dir")).unwrap());
    assert_eq!(
        rejection(create(&root, "raced-dir", 3, input)),
        Some(Rejection::Unsafe)
    );
    assert_eq!(fs::read_dir(root.join("raced-dir")).unwrap().count(), 0);
    // A missing parent becomes a symlink before it is created.
    let link = root.join("parent");
    let input = racing(b"api", || symlink(&outside, &link).unwrap());
    assert_eq!(
        rejection(create(&root, "parent/child/file", 3, input)),
        Some(Rejection::Unsafe)
    );
    assert_eq!(fs::read_dir(&outside).unwrap().count(), 0);
    // A missing parent is created concurrently as a real directory.
    let concurrent = root.join("shared");
    let input = racing(b"api", || fs::create_dir(&concurrent).unwrap());
    create(&root, "shared/file", 3, input).unwrap();
    assert_eq!(fs::read(root.join("shared/file")).unwrap(), b"api");
    // A held existing ancestor that is replaced by a link keeps the commit inside.
    fs::create_dir(root.join("a")).unwrap();
    let input = racing(b"api", || {
        fs::rename(root.join("a"), root.join("held")).unwrap();
        symlink(&outside, root.join("a")).unwrap();
    });
    create(&root, "a/b/file", 3, input).unwrap();
    assert_eq!(fs::read(root.join("held/b/file")).unwrap(), b"api");
    assert_eq!(fs::read_dir(&outside).unwrap().count(), 0);
}

#[test]
fn link_fallback_never_replaces_and_removes_staging() {
    let f = fixture();
    let staging_root = f.path().join("staging");
    let root = f.path().join("workspace");
    fs::create_dir(&staging_root).unwrap();
    fs::create_dir(&root).unwrap();
    fs::write(root.join("existing"), b"old").unwrap();
    let staging_parent = directory(&anchor(&staging_root).unwrap(), "").unwrap();
    let parent = directory(&anchor(&root).unwrap(), "").unwrap();
    let staging = Staging::new(&staging_parent).unwrap();
    (&staging.file).write_all(b"new").unwrap();
    assert_eq!(
        staging.link_new(&parent, "existing").unwrap_err().rejection,
        Some(Rejection::Unsafe)
    );
    assert_eq!(fs::read(root.join("existing")).unwrap(), b"old");
    assert_eq!(fs::read_dir(&staging_root).unwrap().count(), 0);
    let staging = Staging::new(&staging_parent).unwrap();
    (&staging.file).write_all(b"new").unwrap();
    staging.link_new(&parent, "fresh").unwrap();
    assert_eq!(fs::read(root.join("fresh")).unwrap(), b"new");
    assert_eq!(fs::metadata(root.join("fresh")).unwrap().nlink(), 1);
    assert_eq!(fs::read_dir(&staging_root).unwrap().count(), 0);
}
