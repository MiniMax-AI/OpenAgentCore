use crate::workspace_path::{anchor, directory};
use rustix::fs::{
    AtFlags, FileType, Mode, OFlags, RenameFlags, fstat, linkat, mkdirat, openat, renameat,
    renameat_with, statat, unlinkat,
};
use rustix::io::Errno;
use sha2::{Digest, Sha256};
use std::fs::File;
use std::io::{self, Read, Write};
use std::os::fd::OwnedFd;
use std::path::Path;

pub(crate) const MAX_BYTES: u64 = 50 * 1024 * 1024;

/// Selects the destination policy explicitly for each invocation.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum InstallMode {
    /// Trusted initialization (initial Session files and Skills): the parent must
    /// exist and an existing regular file is replaced by a fresh inode.
    Replace,
    /// Public Environment Files.create: missing parents are created without
    /// following links and an existing destination is never replaced.
    Create,
}

/// A known create-mode refusal. Nothing was installed and staging is removed.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Rejection {
    /// A directory already occupies the destination.
    Directory,
    /// The destination exists, or a path component is a symlink or not a directory.
    Unsafe,
}

#[derive(Debug)]
pub(crate) struct Failure {
    pub committed: bool,
    pub rejection: Option<Rejection>,
    pub error: io::Error,
}

impl From<io::Error> for Failure {
    fn from(error: io::Error) -> Self {
        Self {
            committed: false,
            rejection: None,
            error,
        }
    }
}

impl From<Errno> for Failure {
    fn from(error: Errno) -> Self {
        io::Error::from(error).into()
    }
}

impl From<Rejection> for Failure {
    fn from(rejection: Rejection) -> Self {
        Self {
            committed: false,
            rejection: Some(rejection),
            error: io::ErrorKind::AlreadyExists.into(),
        }
    }
}

pub(crate) fn install(
    root: &Path,
    relative: &str,
    size: u64,
    mut input: impl Read,
    staging_root: &Path,
    mode: InstallMode,
) -> Result<(), Failure> {
    if size > MAX_BYTES
        || staging_root.starts_with(root)
        || root.starts_with(staging_root)
        || relative.is_empty()
        || relative.len() > 4096
        || relative
            .split('/')
            .any(|p| p.is_empty() || p == "." || p == ".." || p.contains(['\\', '\0', '\r', '\n']))
    {
        return Err(io::Error::from(io::ErrorKind::InvalidInput).into());
    }
    let (parent, leaf) = relative.rsplit_once('/').unwrap_or(("", relative));
    let root = anchor(root)?;
    let (held, missing) = match mode {
        InstallMode::Replace => (directory(&root, parent)?, Vec::new()),
        InstallMode::Create => existing_prefix(&root, parent)?,
    };
    let staging_parent = directory(&anchor(staging_root)?, "")?;
    let device = fstat(&staging_parent)?.st_dev;
    if fstat(&held)?.st_dev != device {
        return Err(io::Error::from(io::ErrorKind::InvalidInput).into());
    }
    if missing.is_empty() {
        match (mode, statat(&held, leaf, AtFlags::SYMLINK_NOFOLLOW)) {
            (_, Err(Errno::NOENT)) => (),
            (InstallMode::Replace, Ok(metadata))
                if FileType::from_raw_mode(metadata.st_mode) == FileType::RegularFile => {}
            (InstallMode::Replace, Ok(_)) => {
                return Err(io::Error::from(io::ErrorKind::InvalidInput).into());
            }
            (InstallMode::Create, Ok(metadata))
                if FileType::from_raw_mode(metadata.st_mode) == FileType::Directory =>
            {
                return Err(Rejection::Directory.into());
            }
            (InstallMode::Create, Ok(_)) => return Err(Rejection::Unsafe.into()),
            (_, Err(error)) => return Err(error.into()),
        }
    }
    let staging = Staging::new(&staging_parent)?;
    let mut file = &staging.file;
    let mut digest = Sha256::new();
    let mut remaining = size;
    let mut buffer = [0u8; 64 * 1024];
    while remaining != 0 {
        let count = remaining.min(buffer.len() as u64) as usize;
        input.read_exact(&mut buffer[..count])?;
        file.write_all(&buffer[..count])?;
        digest.update(&buffer[..count]);
        remaining -= count as u64;
    }
    // Native process/write has no stdin-close operation; the digest ends one frame.
    let mut trailer = [0u8; 32];
    input.read_exact(&mut trailer)?;
    if digest.finalize().as_slice() != trailer {
        return Err(io::Error::from(io::ErrorKind::InvalidData).into());
    }
    staging.file.sync_all()?;
    let parent = match mode {
        InstallMode::Replace => {
            staging.replace(&held, leaf)?;
            held
        }
        InstallMode::Create => {
            // Parents are created only after the complete body is verified.
            let parent = create_missing(held, &missing)?;
            if fstat(&parent)?.st_dev != device {
                return Err(io::Error::from(io::ErrorKind::InvalidInput).into());
            }
            staging.install_new(&parent, leaf)?;
            parent
        }
    };
    File::from(parent).sync_all().map_err(|error| Failure {
        committed: true,
        rejection: None,
        error,
    })?;
    File::from(staging_parent)
        .sync_all()
        .map_err(|error| Failure {
            committed: true,
            rejection: None,
            error,
        })?;
    Ok(())
}

const HELD: OFlags = OFlags::PATH
    .union(OFlags::DIRECTORY)
    .union(OFlags::NOFOLLOW)
    .union(OFlags::CLOEXEC);

/// Walks existing components below the held root without following links. It
/// returns the deepest existing directory and the components still missing. An
/// existing symlink or non-directory component rejects; nothing is created here.
fn existing_prefix<'a>(
    root: &OwnedFd,
    relative: &'a str,
) -> Result<(OwnedFd, Vec<&'a str>), Failure> {
    let mut fd = openat(
        root,
        ".",
        OFlags::PATH | OFlags::DIRECTORY | OFlags::CLOEXEC,
        Mode::empty(),
    )?;
    if relative.is_empty() {
        return Ok((fd, Vec::new()));
    }
    let parts: Vec<_> = relative.split('/').collect();
    for (index, part) in parts.iter().enumerate() {
        match openat(&fd, *part, HELD, Mode::empty()) {
            Ok(child) => fd = child,
            Err(Errno::NOENT) => return Ok((fd, parts[index..].to_vec())),
            Err(Errno::NOTDIR | Errno::LOOP) => return Err(Rejection::Unsafe.into()),
            Err(error) => return Err(error.into()),
        }
    }
    Ok((fd, Vec::new()))
}

/// Creates each missing component with mode 0700 relative to the held parent,
/// then reopens it without following links. A component that appears
/// concurrently is accepted only if it is a real directory.
fn create_missing(mut fd: OwnedFd, missing: &[&str]) -> Result<OwnedFd, Failure> {
    for part in missing {
        match mkdirat(&fd, *part, Mode::from_raw_mode(0o700)) {
            Ok(()) => File::from(openat(
                &fd,
                ".",
                OFlags::RDONLY | OFlags::DIRECTORY | OFlags::CLOEXEC,
                Mode::empty(),
            )?)
            .sync_all()?,
            Err(Errno::EXIST) => (),
            Err(error) => return Err(error.into()),
        }
        fd = match openat(&fd, *part, HELD, Mode::empty()) {
            Ok(child) => child,
            Err(Errno::NOTDIR | Errno::LOOP) => return Err(Rejection::Unsafe.into()),
            Err(error) => return Err(error.into()),
        };
    }
    Ok(openat(
        &fd,
        ".",
        OFlags::RDONLY | OFlags::DIRECTORY | OFlags::CLOEXEC,
        Mode::empty(),
    )?)
}

struct Staging<'a> {
    parent: &'a OwnedFd,
    name: String,
    file: File,
    committed: bool,
}

impl<'a> Staging<'a> {
    fn new(parent: &'a OwnedFd) -> io::Result<Self> {
        let name = format!(".parsar-upload-{}", uuid::Uuid::new_v4());
        let file = openat(
            parent,
            name.as_str(),
            OFlags::WRONLY | OFlags::CREATE | OFlags::EXCL | OFlags::NOFOLLOW | OFlags::CLOEXEC,
            Mode::from_raw_mode(0o600),
        )?;
        Ok(Self {
            parent,
            name,
            file: File::from(file),
            committed: false,
        })
    }

    fn replace(mut self, destination: &OwnedFd, leaf: &str) -> io::Result<()> {
        renameat(self.parent, self.name.as_str(), destination, leaf)?;
        self.committed = true;
        Ok(())
    }

    /// Atomically installs the file only if the destination name is free.
    fn install_new(self, destination: &OwnedFd, leaf: &str) -> Result<(), Failure> {
        match renameat_with(
            self.parent,
            self.name.as_str(),
            destination,
            leaf,
            RenameFlags::NOREPLACE,
        ) {
            Ok(()) => {
                self.commit();
                Ok(())
            }
            Err(Errno::EXIST) => Err(Rejection::Unsafe.into()),
            // The filesystem or kernel lacks RENAME_NOREPLACE.
            Err(Errno::INVAL | Errno::NOSYS) => self.link_new(destination, leaf),
            Err(error) => Err(error.into()),
        }
    }

    /// linkat never replaces an existing name. Removing the staging name after
    /// the link is best-effort cleanup of an already installed file.
    fn link_new(self, destination: &OwnedFd, leaf: &str) -> Result<(), Failure> {
        match linkat(
            self.parent,
            self.name.as_str(),
            destination,
            leaf,
            AtFlags::empty(),
        ) {
            Ok(()) => {
                let _ = unlinkat(self.parent, self.name.as_str(), AtFlags::empty());
                self.commit();
                Ok(())
            }
            Err(Errno::EXIST) => Err(Rejection::Unsafe.into()),
            Err(error) => Err(error.into()),
        }
    }

    fn commit(mut self) {
        self.committed = true;
    }
}

impl Drop for Staging<'_> {
    fn drop(&mut self) {
        if !self.committed {
            let _ = unlinkat(self.parent, self.name.as_str(), AtFlags::empty());
        }
    }
}

#[cfg(test)]
#[path = "write_file_tests.rs"]
mod tests;
