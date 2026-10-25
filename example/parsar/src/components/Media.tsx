import { useMutation } from "@tanstack/react-query";
import { downloadArtifact } from "../lib/api";
import { ErrorNotice } from "./shared";
import { useEffect, useState } from "react";
import type { SessionArtifact } from "@oac/agents-client";
import { imageMime, readMediaBytes, artifactContentPath } from "../../shared/media.mjs";
import { tr } from "../lib/i18n";

// Blob URLs keep HTML/SVG artifacts downloadable without executing them in chat.
export function MediaImage({ src, alt }: { src: string; alt: string }) {
  const [preview, setPreview] = useState<{ src: string; url?: string; error?: string }>();
  useEffect(() => {
    const controller = new AbortController();
    let url: string | undefined;
    void (async () => {
      const response = await fetch(src, { signal: controller.signal });
      if (!response.ok) {
        const value = await response.json().catch(() => ({}));
        throw new Error(value.error?.message || tr("图片暂不可用，可下载原文件。"));
      }
      const bytes = await readMediaBytes(response);
      const type = imageMime(bytes);
      if (!type) throw new Error(tr("此格式不支持预览，请下载文件查看。"));
      if (controller.signal.aborted) return;
      url = URL.createObjectURL(new Blob([bytes], { type }));
      setPreview({ src, url });
    })().catch((error) => {
      if (!controller.signal.aborted) setPreview({ src, error: error.message });
    });
    return () => { controller.abort(); if (url) URL.revokeObjectURL(url); };
  }, [src]);
  const current = preview?.src === src ? preview : undefined;
  return current?.url ? (
    <a href={src} target="_blank" rel="noreferrer" className="inline-block max-w-full">
      <img src={current.url} alt={alt} className="my-2 max-h-96 max-w-full rounded-lg object-contain" />
    </a>
  ) : (
    <span role="status" className="block text-sm text-fg-muted">{current?.error || tr("正在加载图片…")}</span>
  );
}

export function ArtifactAttachment({ sessionId, file, preview = true }: {
  sessionId: string; file: SessionArtifact; preview?: boolean;
}) {
  const download = useMutation({ mutationFn: () => downloadArtifact(sessionId, file) });
  const name = file.path.split("/").at(-1) || "file";
  const src = artifactContentPath(sessionId, file.id);
  return (
    <div className="my-3 max-w-full rounded-xl border border-line p-3">
      {preview && /\.(png|jpe?g|gif|webp)$/i.test(name) && <MediaImage src={src} alt={name} />}
      <button type="button" onClick={() => download.mutate()} disabled={download.isPending} className="cursor-pointer break-all text-left text-sm underline underline-offset-4" aria-label={tr("下载 {{value0}}", { value0: name })}>{name}</button>
      <span className="ml-2 text-xs text-fg-muted">{Math.ceil(file.size_bytes / 1024)} KiB</span>
      <ErrorNotice error={download.error} />
    </div>
  );
}
