import {
  uploadsCancel,
  uploadsComplete,
  uploadsCreate,
  uploadsGet,
} from "./generated/uploads/uploads";
import type { Upload } from "./generated/model";
import { ApiError } from "./fetcher";
import { encryptFile, fileEncryptionFormat } from "../lib/file-crypto";
import { encryptName, type LibraryKeys, nameToken } from "../lib/library-crypto";

const plainChunkSize = 16 * 1024 * 1024;
const maxRetries = 5;

type UploadTarget = {
  libraryId: string;
  parentId: string;
  targetNodeId?: string;
  expectedRevision?: number;
};

type UploadProgress = (uploaded: number, total: number) => void;
export type UploadPhase = "uploading" | "publishing";

export type UploadOptions = {
  signal?: AbortSignal;
  onProgress?: UploadProgress;
  onSession?: (session: Upload) => void;
  onPhase?: (phase: UploadPhase) => void;
};

export async function uploadPlainFile(
  file: File,
  target: UploadTarget,
  options: UploadOptions = {},
) {
  const session = await uploadsCreate(
    {
      library_id: target.libraryId,
      parent_id: target.parentId,
      target_node_id: target.targetNodeId,
      expected_revision: target.expectedRevision,
      name: file.name,
      size: file.size,
    },
    { signal: options.signal },
  );
  options.onSession?.(session);
  options.onPhase?.("uploading");
  await uploadBlob(session, file, options);
  options.onPhase?.("publishing");
  const finalizing = await uploadsComplete(session.id, {}, { signal: options.signal });
  return waitForPublication(finalizing, options.signal);
}

export async function uploadEncryptedFile(
  file: File,
  target: UploadTarget,
  keys: LibraryKeys,
  options: UploadOptions = {},
) {
  const [encrypted, encryptedName, token] = await Promise.all([
    encryptFile(file, keys),
    encryptName(keys, file.name),
    nameToken(keys, target.parentId, file.name),
  ]);
  const session = await uploadsCreate(
    {
      library_id: target.libraryId,
      parent_id: target.parentId,
      target_node_id: target.targetNodeId,
      expected_revision: target.expectedRevision,
      encrypted_name: encryptedName,
      name_token: token,
      size: encrypted.size,
    },
    { signal: options.signal },
  );
  options.onSession?.(session);
  options.onPhase?.("uploading");
  await uploadStream(session, encrypted.stream, options);
  const metadata = await encrypted.metadata;
  options.onPhase?.("publishing");
  const finalizing = await uploadsComplete(
    session.id,
    {
      dedup_fingerprint: metadata.dedupFingerprint,
      encrypted_file_key: encrypted.encryptedFileKey,
      encryption_format: fileEncryptionFormat,
    },
    { signal: options.signal },
  );
  return waitForPublication(finalizing, options.signal);
}

export async function cancelUpload(id: string) {
  await uploadsCancel(id);
}

async function waitForPublication(upload: Upload, signal?: AbortSignal) {
  let current = upload;
  let delay = 250;
  while (current.state === "finalizing" || current.state === "uploaded") {
    await abortableDelay(delay, signal);
    current = await uploadsGet(current.id, { signal });
    delay = Math.min(delay * 2, 2_000);
  }
  if (current.state !== "completed") {
    throw new Error(current.failure_message ?? `The upload ended with state ${current.state}.`);
  }
  return current;
}

function abortableDelay(milliseconds: number, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason ?? new DOMException("The upload was cancelled.", "AbortError"));
      return;
    }
    const finish = () => {
      signal?.removeEventListener("abort", abort);
      resolve();
    };
    const abort = () => {
      window.clearTimeout(timer);
      reject(signal?.reason ?? new DOMException("The upload was cancelled.", "AbortError"));
    };
    const timer = window.setTimeout(finish, milliseconds);
    signal?.addEventListener("abort", abort, { once: true });
  });
}

async function uploadBlob(session: Upload, file: Blob, options: UploadOptions) {
  let offset = await remoteOffset(session.upload_url, options.signal);
  options.onProgress?.(offset, session.declared_size);
  while (offset < file.size) {
    const chunk = file.slice(offset, Math.min(file.size, offset + plainChunkSize));
    offset = await patchWithRetry(
      session.upload_url,
      offset,
      new Uint8Array(await chunk.arrayBuffer()),
      options.signal,
    );
    options.onProgress?.(offset, session.declared_size);
  }
}

async function uploadStream(
  session: Upload,
  stream: ReadableStream<Uint8Array>,
  options: UploadOptions,
) {
  let offset = await remoteOffset(session.upload_url, options.signal);
  options.onProgress?.(offset, session.declared_size);
  let generated = 0;
  for await (const chunk of stream) {
    const start = generated;
    generated += chunk.byteLength;
    if (generated <= offset) continue;
    if (start !== offset) {
      throw new Error("The encrypted upload offset is not on a frame boundary.");
    }
    offset = await patchWithRetry(session.upload_url, offset, chunk, options.signal);
    options.onProgress?.(offset, session.declared_size);
  }
}

async function patchWithRetry(
  url: string,
  initialOffset: number,
  body: Uint8Array,
  signal?: AbortSignal,
) {
  let offset = initialOffset;
  for (let attempt = 0; attempt < maxRetries; attempt += 1) {
    try {
      const response = await fetch(url, {
        method: "PATCH",
        credentials: "same-origin",
        signal,
        headers: {
          "Content-Type": "application/offset+octet-stream",
          "Tus-Resumable": "1.0.0",
          "Upload-Offset": String(offset),
        },
        body: new Blob([Uint8Array.from(body)]),
      });
      if (!response.ok) throw await responseError(response);
      return headerNumber(response, "Upload-Offset");
    } catch (error) {
      if (signal?.aborted || attempt === maxRetries - 1) throw error;
      const current = await remoteOffset(url, signal);
      if (current === initialOffset + body.byteLength) return current;
      if (current !== initialOffset)
        throw new Error("The server returned an unexpected upload offset.");
      offset = current;
    }
  }
  throw new Error("The upload retry limit was reached.");
}

async function remoteOffset(url: string, signal?: AbortSignal) {
  const response = await fetch(url, {
    method: "HEAD",
    credentials: "same-origin",
    signal,
    headers: { "Tus-Resumable": "1.0.0" },
  });
  if (!response.ok) throw await responseError(response);
  return headerNumber(response, "Upload-Offset");
}

function headerNumber(response: Response, name: string) {
  const value = Number(response.headers.get(name));
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error(`The server returned an invalid ${name} header.`);
  }
  return value;
}

async function responseError(response: Response) {
  let problem: object | undefined;
  try {
    problem = (await response.json()) as object;
  } catch {
    problem = undefined;
  }
  return new ApiError(response.status, problem);
}
