import { getFilesContentUrl } from "./generated/files/files";
import type { FileDetails, Problem } from "./generated/model";
import { ApiError } from "./fetcher";
import { decryptFileStream } from "../lib/file-crypto";
import type { LibraryKeys } from "../lib/library-crypto";

export type PreviewKind = "image" | "video" | "audio" | "pdf" | "text";

export type Preview =
  | { kind: Exclude<PreviewKind, "text">; url: string; revoke: boolean }
  | { kind: "text"; text: string };

type FileType = { kind: PreviewKind; type: string };

const textPreviewLimit = 2 * 1024 * 1024;
const decryptedPreviewLimit = 128 * 1024 * 1024;

const textExtensions = [
  "txt",
  "md",
  "markdown",
  "csv",
  "json",
  "xml",
  "yaml",
  "yml",
  "log",
  "css",
  "js",
  "jsx",
  "ts",
  "tsx",
  "go",
  "rs",
  "py",
  "sh",
  "toml",
  "ini",
];

const fileTypes: Record<string, FileType> = {
  png: { kind: "image", type: "image/png" },
  jpg: { kind: "image", type: "image/jpeg" },
  jpeg: { kind: "image", type: "image/jpeg" },
  gif: { kind: "image", type: "image/gif" },
  webp: { kind: "image", type: "image/webp" },
  avif: { kind: "image", type: "image/avif" },
  bmp: { kind: "image", type: "image/bmp" },
  mp4: { kind: "video", type: "video/mp4" },
  m4v: { kind: "video", type: "video/mp4" },
  mov: { kind: "video", type: "video/quicktime" },
  webm: { kind: "video", type: "video/webm" },
  ogv: { kind: "video", type: "video/ogg" },
  mp3: { kind: "audio", type: "audio/mpeg" },
  wav: { kind: "audio", type: "audio/wav" },
  ogg: { kind: "audio", type: "audio/ogg" },
  oga: { kind: "audio", type: "audio/ogg" },
  m4a: { kind: "audio", type: "audio/mp4" },
  aac: { kind: "audio", type: "audio/aac" },
  flac: { kind: "audio", type: "audio/flac" },
  pdf: { kind: "pdf", type: "application/pdf" },
  ...Object.fromEntries(
    textExtensions.map((extension): [string, FileType] => [
      extension,
      { kind: "text", type: "text/plain;charset=utf-8" },
    ]),
  ),
};

function fileType(name: string): FileType | undefined {
  const index = name.lastIndexOf(".");
  return index < 0 ? undefined : fileTypes[name.slice(index + 1).toLowerCase()];
}

export function previewKind(name: string) {
  return fileType(name)?.kind;
}

// Returns the reason when the file has no preview.
export function previewUnavailable(details: FileDetails, name: string) {
  const kind = previewKind(name);
  if (!kind) return "This file type does not have a preview.";
  if (kind === "text" && details.size > textPreviewLimit) {
    return "Text previews are limited to 2 MiB.";
  }
  // The browser holds a decrypted preview in memory.
  if (details.encryption_format && details.size > decryptedPreviewLimit) {
    return "Encrypted previews are limited to 128 MiB.";
  }
  return undefined;
}

export async function loadPreview(
  details: FileDetails,
  name: string,
  keys: LibraryKeys | undefined,
  signal?: AbortSignal,
): Promise<Preview> {
  const kind = previewKind(name);
  const unavailable = previewUnavailable(details, name);
  if (!kind || unavailable) throw new Error(unavailable);
  if (kind === "text") {
    const blob = await loadBlob(details, name, keys, signal);
    return { kind, text: await blob.text() };
  }
  if (!details.encryption_format) {
    return { kind, url: contentURL(details, "inline"), revoke: false };
  }
  const blob = await loadBlob(details, name, keys, signal);
  return { kind, url: URL.createObjectURL(blob), revoke: true };
}

// Returns false when the user cancels the save dialog.
export async function downloadFile(details: FileDetails, name: string, keys?: LibraryKeys) {
  if (!details.encryption_format) {
    triggerDownload(contentURL(details, "attachment"), name);
    return true;
  }
  if (!keys || !details.encrypted_file_key) {
    throw new Error("Unlock the library before you download this file.");
  }
  let target: SaveHandle | undefined;
  try {
    target = await chooseSaveTarget(name);
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") return false;
    throw error;
  }
  const response = await fetchContent(details, "attachment");
  if (!response.body) throw new Error("The browser cannot stream this download.");
  const decrypted = await decryptFileStream(
    response.body,
    keys,
    details.encrypted_file_key,
    details.stored_size,
  );
  if (target) {
    await decrypted.stream.pipeTo(await target.createWritable());
    return true;
  }
  // Without a save dialog, the browser holds the decrypted file in memory.
  const blob = await new Response(decrypted.stream).blob();
  const url = URL.createObjectURL(new Blob([blob], { type: mimeType(name) }));
  triggerDownload(url, name);
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
  return true;
}

async function loadBlob(
  details: FileDetails,
  name: string,
  keys: LibraryKeys | undefined,
  signal?: AbortSignal,
) {
  const response = await fetchContent(details, "inline", signal);
  if (!details.encryption_format) return response.blob();
  if (!keys || !details.encrypted_file_key) {
    throw new Error("Unlock the library before you preview this file.");
  }
  if (!response.body) throw new Error("The browser cannot decrypt this preview.");
  const decrypted = await decryptFileStream(
    response.body,
    keys,
    details.encrypted_file_key,
    details.stored_size,
  );
  const blob = await new Response(decrypted.stream).blob();
  return new Blob([blob], { type: mimeType(name) });
}

function contentURL(details: FileDetails, disposition: "inline" | "attachment") {
  return getFilesContentUrl(details.id, { disposition });
}

async function fetchContent(
  details: FileDetails,
  disposition: "inline" | "attachment",
  signal?: AbortSignal,
) {
  const response = await fetch(contentURL(details, disposition), {
    credentials: "same-origin",
    signal,
  });
  if (response.ok) return response;
  let problem: Problem | undefined;
  try {
    problem = (await response.json()) as Problem;
  } catch {
    problem = undefined;
  }
  throw new ApiError(response.status, problem);
}

function triggerDownload(url: string, name: string) {
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  anchor.click();
}

type SaveHandle = {
  createWritable(): Promise<WritableStream<Uint8Array>>;
};

type SavePicker = (options: { suggestedName: string }) => Promise<SaveHandle>;

async function chooseSaveTarget(name: string) {
  const picker = (window as Window & { showSaveFilePicker?: SavePicker }).showSaveFilePicker;
  return picker?.({ suggestedName: name });
}

function mimeType(name: string) {
  return fileType(name)?.type ?? "application/octet-stream";
}
