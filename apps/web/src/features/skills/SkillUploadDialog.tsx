import { Upload } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import type { AgentCore, Skill, SkillUploadInput, SkillVersion } from "@agents-core-web/agents-client";

import { HelpTip, SegmentedControl } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { formatBytes, MISSING } from "../../lib/format";
import {
  folderFilesFromSelection,
  previewSkillFolder,
  previewSkillZip,
  type SkillBundleIssue,
  type SkillBundlePreview,
  type SkillFolderFile,
} from "./skill-bundle";
import { mapSkillUploadError, type SkillUploadFailure } from "./skill-operations";

export type SkillUploadTarget = { kind: "skill" } | { kind: "version"; skill: Skill };
export type SkillUploadResult = { kind: "skill"; skill: Skill } | { kind: "version"; version: SkillVersion };
type Mode = "zip" | "directory";
type Selection = { kind: "zip"; file: File } | { kind: "directory"; files: SkillFolderFile[] };
type SkillsT = TFunction<"skills">;

function joinListed(t: SkillsT, values: readonly string[], more = 0): string {
  const list = values.join(", ");
  return more > 0 ? t("upload.issues.more", { list, count: more }) : list;
}

/** One sentence per preflight finding, with every value shown as plain text. */
export function skillIssueText(t: SkillsT, issue: SkillBundleIssue): string {
  switch (issue.code) {
    case "no-files": return t("upload.issues.noFiles");
    case "too-many-files": return t("upload.issues.tooManyFiles", { count: issue.count, limit: issue.limit });
    case "too-large": return t("upload.issues.tooLarge", { size: formatBytes(issue.bytes), limit: formatBytes(issue.limit) });
    case "zip-too-large": return t("upload.issues.zipTooLarge", { size: formatBytes(issue.bytes), limit: formatBytes(issue.limit) });
    case "not-zip": return t("upload.issues.notZip");
    case "multiple-top-level": return t("upload.issues.multipleTopLevel", { names: issue.names.join(", ") });
    case "outside-folder": return t("upload.issues.outsideFolder", { paths: joinListed(t, issue.paths, issue.more) });
    case "missing-manifest": return t("upload.issues.missingManifest", { folder: issue.folder });
    case "invalid-path": return t("upload.issues.invalidPath", { paths: joinListed(t, issue.paths.map((path) => JSON.stringify(path)), issue.more) });
    case "duplicate-path": return t("upload.issues.duplicatePath", { paths: joinListed(t, issue.paths, issue.more) });
    case "hidden-files": return t("upload.issues.hiddenFiles", { paths: joinListed(t, issue.paths, issue.more) });
    case "manifest-unreadable": return t("upload.issues.manifestUnreadable");
    case "zip-unreadable": return t("upload.issues.zipUnreadable");
    case "frontmatter-missing": return t("upload.issues.frontmatterMissing");
    case "frontmatter-invalid": return t("upload.issues.frontmatterInvalid");
    case "frontmatter-field-missing": return issue.field === "name" ? t("upload.issues.nameMissing") : t("upload.issues.descriptionMissing");
    case "frontmatter-unsupported": return t("upload.issues.unsupportedKeys", { keys: issue.keys.join(", ") });
    case "name-format": return t("upload.issues.nameFormat");
  }
}

/** The dialog message for a failed upload (spec table: 400, 413, 503, network or cancel). */
export function skillUploadFailureText(t: SkillsT, failure: SkillUploadFailure): string {
  switch (failure.kind) {
    case "invalid": return t("upload.errors.invalid", { message: failure.message });
    case "too-large": return t("upload.errors.tooLarge");
    case "storage-unavailable": return t("upload.errors.storage");
    case "interrupted": return failure.cancelled ? t("upload.errors.cancelled") : t("upload.errors.interrupted");
    case "other": return t("upload.errors.other", { message: failure.message });
  }
}

function unreadablePreview(selection: Selection): SkillBundlePreview {
  return {
    kind: selection.kind,
    topLevel: null,
    fileCount: selection.kind === "directory" ? selection.files.length : null,
    totalBytes: null,
    archiveBytes: selection.kind === "zip" ? selection.file.size : null,
    name: null,
    description: null,
    errors: [],
    warnings: [{ code: selection.kind === "zip" ? "zip-unreadable" : "manifest-unreadable" }],
  };
}

export function SkillBundlePreviewView({ preview }: { preview: SkillBundlePreview }) {
  const { t } = useTranslation("skills");
  return (
    <div className="skill-preview" aria-label={t("upload.preview.label")} role="group">
      <dl>
        <div><dt>{t("upload.preview.folder")}</dt><dd>{preview.topLevel ?? MISSING}</dd></div>
        <div><dt>{t("upload.preview.files")}</dt><dd>{preview.fileCount ?? MISSING}</dd></div>
        {preview.kind === "zip" ? <div><dt>{t("upload.preview.archive")}</dt><dd>{formatBytes(preview.archiveBytes)}</dd></div> : null}
        <div><dt>{t("upload.preview.size")}</dt><dd>{formatBytes(preview.totalBytes)}</dd></div>
        <div><dt>{t("upload.preview.name")}</dt><dd>{preview.name ?? t("upload.preview.missing")}</dd></div>
        <div><dt>{t("upload.preview.description")}</dt><dd className="skill-preview-text">{preview.description ?? t("upload.preview.missing")}</dd></div>
      </dl>
      {preview.errors.length ? (
        <ul className="skill-issues skill-issues-error" role="alert">
          {preview.errors.map((issue, index) => <li key={index}>{skillIssueText(t, issue)}</li>)}
        </ul>
      ) : null}
      {preview.warnings.length ? (
        <ul className="skill-issues">
          {preview.warnings.map((issue, index) => <li key={index}>{skillIssueText(t, issue)}</li>)}
        </ul>
      ) : null}
    </div>
  );
}

export function SkillRequirements({ open, onToggle }: { open: boolean; onToggle: (open: boolean) => void }) {
  const { t } = useTranslation("skills");
  return (
    <details className="skill-requirements" open={open} onToggle={(event) => onToggle(event.currentTarget.open)}>
      <summary>{t("upload.requirements.title")}</summary>
      <ul>
        <li>{t("upload.requirements.folder")}</li>
        <li>{t("upload.requirements.frontmatter")}</li>
        <li>{t("upload.requirements.files")}</li>
        <li>{t("upload.requirements.limits")}</li>
      </ul>
      <p>{t("upload.requirements.authority")}</p>
    </details>
  );
}

export interface SkillUploadDialogProps {
  open: boolean;
  target: SkillUploadTarget;
  core: Pick<AgentCore, "uploadSkill" | "uploadSkillVersion">;
  onClose: () => void;
  onUploaded: (result: SkillUploadResult) => void;
  /** An upload ended without an answer; Core may still have stored it. */
  onInterrupted?: () => void;
}

/**
 * Uploads a new Skill, or a new version of one, from a ZIP or a folder. The
 * dialog cannot be dismissed while a request is in flight; Cancel upload aborts it.
 */
export function SkillUploadDialog({ open, target, core, onClose, onUploaded, onInterrupted }: SkillUploadDialogProps) {
  const { t } = useTranslation("skills");
  const { t: tCommon } = useTranslation();
  const [mode, setMode] = useState<Mode>("zip");
  const [selection, setSelection] = useState<Selection | null>(null);
  const [preview, setPreview] = useState<SkillBundlePreview | null>(null);
  const [reading, setReading] = useState(false);
  const [makeDefault, setMakeDefault] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [failure, setFailure] = useState<SkillUploadFailure | null>(null);
  const [requirementsOpen, setRequirementsOpen] = useState(false);
  const mounted = useRef(true);
  const previewRequest = useRef(0);
  const abortRef = useRef<AbortController | null>(null);
  const cancelledRef = useRef(false);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      abortRef.current?.abort();
    };
  }, []);

  const choose = async (next: Selection | null) => {
    const request = previewRequest.current + 1;
    previewRequest.current = request;
    setSelection(next);
    setPreview(null);
    setFailure(null);
    setReading(Boolean(next));
    if (!next) return;
    let result: SkillBundlePreview;
    try {
      result = next.kind === "zip" ? await previewSkillZip(next.file, next.file.name) : await previewSkillFolder(next.files);
    } catch {
      result = unreadablePreview(next);
    }
    if (!mounted.current || request !== previewRequest.current) return;
    setPreview(result);
    setReading(false);
  };

  const changeMode = (next: Mode) => {
    if (uploading || next === mode) return;
    setMode(next);
    void choose(null);
  };

  const upload = async () => {
    if (!selection || !preview || preview.errors.length || abortRef.current || reading) return;
    const controller = new AbortController();
    abortRef.current = controller;
    cancelledRef.current = false;
    setUploading(true);
    setFailure(null);
    const input: SkillUploadInput = selection.kind === "zip"
      ? { kind: "zip", file: selection.file, filename: selection.file.name }
      : { kind: "directory", files: selection.files.map(({ path, file }) => ({ path, file })) };
    try {
      if (target.kind === "skill") {
        const skill = await core.uploadSkill(input, { signal: controller.signal });
        if (mounted.current) onUploaded({ kind: "skill", skill });
      } else {
        const version = await core.uploadSkillVersion(target.skill.id, input, {
          signal: controller.signal,
          // Core rejects a repeated default field, so it is sent once and only when chosen.
          ...(makeDefault ? { setDefault: true } : {}),
        });
        if (mounted.current) onUploaded({ kind: "version", version });
      }
    } catch (error) {
      const mapped = mapSkillUploadError(error, cancelledRef.current);
      if (mapped.kind === "interrupted") onInterrupted?.();
      if (mounted.current) setFailure(mapped);
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
      if (mounted.current) setUploading(false);
    }
  };

  const cancelUpload = () => {
    cancelledRef.current = true;
    abortRef.current?.abort();
  };

  const close = () => {
    if (!uploading) onClose();
  };

  const canUpload = Boolean(selection && preview && !preview.errors.length && !reading && !uploading);
  const title = target.kind === "skill" ? t("upload.title") : t("upload.versionTitle", { name: target.skill.name });

  return (
    <Modal
      open={open}
      title={title}
      onClose={close}
      footer={uploading ? (
        <>
          <button className="button outline" type="button" onClick={cancelUpload}>{t("upload.cancelUpload")}</button>
          <button className="button primary" type="button" disabled aria-busy="true">{t("upload.uploading")}</button>
        </>
      ) : (
        <>
          <button className="button outline" type="button" onClick={close}>{tCommon("actions.cancel")}</button>
          <button className="button primary" type="button" disabled={!canUpload} onClick={() => void upload()}>
            <Upload size={14} aria-hidden="true" />{t("upload.submit")}
          </button>
        </>
      )}
    >
      <div className="skill-upload">
        <SegmentedControl
          label={t("upload.source")}
          value={mode}
          onChange={changeMode}
          options={[{ value: "zip", label: t("upload.zip") }, { value: "directory", label: t("upload.folder") }]}
        />
        {mode === "zip" ? (
          <label className="field">
            <span>{t("upload.zipInput")}</span>
            <input
              key="zip"
              type="file"
              accept=".zip,application/zip"
              disabled={uploading}
              onChange={(event) => {
                const file = event.target.files?.[0];
                void choose(file ? { kind: "zip", file } : null);
              }}
            />
          </label>
        ) : (
          <label className="field">
            <span>{t("upload.folderInput")}</span>
            <input
              key="directory"
              ref={(input) => { if (input) input.webkitdirectory = true; }}
              type="file"
              multiple
              disabled={uploading}
              onChange={(event) => {
                const files = event.target.files;
                void choose(files?.length ? { kind: "directory", files: folderFilesFromSelection(files) } : null);
              }}
            />
          </label>
        )}
        {reading ? <p className="page-status" role="status">{t("upload.reading")}</p> : null}
        {preview ? <SkillBundlePreviewView preview={preview} /> : null}
        {target.kind === "version" ? (
          <label className="skill-checkbox">
            <input type="checkbox" checked={makeDefault} disabled={uploading} onChange={(event) => setMakeDefault(event.target.checked)} />
            <span>{t("upload.setDefault")}</span>
            <HelpTip>{t("upload.setDefaultHelp")}</HelpTip>
          </label>
        ) : null}
        {failure ? (
          <div className="coverage-note coverage-note-error skill-upload-failure" role="alert">
            <span>{skillUploadFailureText(t, failure)}</span>
            {failure.kind === "invalid" ? (
              <button type="button" className="text-action" onClick={() => setRequirementsOpen(true)}>{t("upload.errors.seeRequirements")}</button>
            ) : null}
          </div>
        ) : null}
        <SkillRequirements open={requirementsOpen} onToggle={setRequirementsOpen} />
      </div>
    </Modal>
  );
}
