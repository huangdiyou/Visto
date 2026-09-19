import {
  type ChangeEvent,
  type ClipboardEvent,
  type Dispatch,
  type DragEvent,
  type SetStateAction,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { ImagePlus, X } from "lucide-react";
import type { ReviewCommentAttachment } from "@review-studio/contracts";

export interface CommentAttachmentDraft {
  id: string;
  filename: string;
  previewUrl: string;
  localPreviewUrl?: string;
  attachment?: ReviewCommentAttachment;
  error?: string;
  uploading: boolean;
}

export function attachmentIds(items: CommentAttachmentDraft[]): string[] {
  return items
    .map((item) => item.attachment?.id)
    .filter((value): value is string => Boolean(value));
}

const MAX_COMMENT_ATTACHMENTS = 4;
const COMMENT_ATTACHMENT_MAX_UPLOAD_BYTES = 10 * 1024 * 1024;
const COMMENT_ATTACHMENT_PREPROCESS_LIMIT_BYTES = 50 * 1024 * 1024;
const COMMENT_ATTACHMENT_COMPRESS_THRESHOLD_BYTES = 2 * 1024 * 1024;
const COMMENT_ATTACHMENT_MAX_DIMENSION = 2400;
const COMMENT_ATTACHMENT_ERROR_PREVIEW =
  "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='88' height='88' viewBox='0 0 88 88'%3E%3Crect width='88' height='88' rx='10' fill='%23fff5f4'/%3E%3Cpath d='M44 18 72 68H16L44 18Z' fill='none' stroke='%23d55252' stroke-width='5' stroke-linejoin='round'/%3E%3Cpath d='M44 35v14' stroke='%23d55252' stroke-width='5' stroke-linecap='round'/%3E%3Ccircle cx='44' cy='60' r='3' fill='%23d55252'/%3E%3C/svg%3E";
const SUPPORTED_COMMENT_ATTACHMENT_MIMES = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
]);
const SUPPORTED_COMMENT_ATTACHMENT_EXTENSIONS = new Set([
  "png",
  "jpg",
  "jpeg",
  "webp",
  "gif",
]);
const POTENTIAL_COMMENT_ATTACHMENT_EXTENSIONS = new Set([
  ...SUPPORTED_COMMENT_ATTACHMENT_EXTENSIONS,
  "heic",
  "heif",
  "tif",
  "tiff",
]);

export function collectClipboardImageFiles(
  clipboardData: Pick<DataTransfer, "files" | "items"> &
    Partial<Pick<DataTransfer, "getData" | "types">>,
): File[] {
  const filesFromItems = Array.from(clipboardData.items)
    .filter((item) => item.kind === "file")
    .map((item) => item.getAsFile())
    .filter((file): file is File => Boolean(file))
    .filter((file) => isPotentialCommentImageFile(file, true));

  if (filesFromItems.length > 0) {
    return filesFromItems.map(renameClipboardImage);
  }

  return Array.from(clipboardData.files)
    .filter((file) => isPotentialCommentImageFile(file, true))
    .map(renameClipboardImage)
    .concat(collectHTMLImageFiles(clipboardData));
}

export async function collectAsyncClipboardImageFiles(
  clipboard: Pick<Clipboard, "read"> | undefined,
): Promise<File[]> {
  if (!clipboard?.read) {
    throw new Error("clipboard image read unavailable");
  }
  const items = await clipboard.read();
  const files: File[] = [];
  for (const item of items) {
    const imageType =
      item.types.find((type) =>
        SUPPORTED_COMMENT_ATTACHMENT_MIMES.has(type.toLowerCase()),
      ) ?? item.types.find((type) => type.toLowerCase().startsWith("image/"));
    if (!imageType) {
      continue;
    }
    const blob = await item.getType(imageType);
    const file = new File(
      [blob],
      `clipboard-image-${files.length + 1}.${imageExtensionFromMime(
        blob.type || imageType,
      )}`,
      {
        lastModified: Date.now(),
        type: blob.type || imageType,
      },
    );
    if (isPotentialCommentImageFile(file, true)) {
      files.push(renameClipboardImage(file, files.length));
    }
  }
  return files;
}

export function isClipboardImageReadRestricted(
  currentLocation: Pick<Location, "hostname"> | undefined = typeof window ===
  "undefined"
    ? undefined
    : window.location,
  secureContext = typeof window === "undefined" ? true : window.isSecureContext,
) {
  if (secureContext) {
    return false;
  }
  const hostname = currentLocation?.hostname.toLowerCase() ?? "";
  return (
    hostname !== "localhost" && hostname !== "127.0.0.1" && hostname !== "::1"
  );
}

export function CommentAttachmentField({
  value,
  maxLength,
  placeholder,
  disabled,
  attachments,
  uploadFile,
  onAttachmentsChange,
  onChange,
  label = "评论",
}: {
  value: string;
  maxLength: number;
  placeholder: string;
  disabled: boolean;
  attachments: CommentAttachmentDraft[];
  uploadFile: (file: File) => Promise<ReviewCommentAttachment>;
  onAttachmentsChange: Dispatch<SetStateAction<CommentAttachmentDraft[]>>;
  onChange: (value: string) => void;
  label?: string;
}) {
  const inputId = useId();
  const [dragActive, setDragActive] = useState(false);
  const attachmentsRef = useRef(attachments);
  const clipboardAccessRestricted = isClipboardImageReadRestricted();

  useEffect(() => {
    attachmentsRef.current = attachments;
  }, [attachments]);

  useEffect(
    () => () => {
      attachmentsRef.current.forEach((item) => {
        if (item.localPreviewUrl) {
          URL.revokeObjectURL(item.localPreviewUrl);
        }
      });
    },
    [],
  );

  async function addFiles(files: File[]) {
    const imageFiles = files.filter((file) =>
      isPotentialCommentImageFile(file),
    );
    if (imageFiles.length === 0 || disabled) {
      return;
    }
    const available = Math.max(0, MAX_COMMENT_ATTACHMENTS - attachments.length);
    const selected = imageFiles.slice(0, available);
    if (selected.length === 0) {
      return;
    }
    const drafts = selected.map((file, index) => {
      const safeFile = renameClipboardImage(file, index);
      const canPreview =
        safeFile.size <= COMMENT_ATTACHMENT_PREPROCESS_LIMIT_BYTES &&
        isSupportedCommentImageFile(safeFile);
      const localPreviewUrl = canPreview
        ? URL.createObjectURL(safeFile)
        : undefined;
      const draft: CommentAttachmentDraft = {
        id: crypto.randomUUID(),
        filename: safeFile.name || "screenshot.png",
        previewUrl: localPreviewUrl ?? COMMENT_ATTACHMENT_ERROR_PREVIEW,
        uploading: true,
      };
      if (localPreviewUrl) {
        draft.localPreviewUrl = localPreviewUrl;
      }
      return draft;
    });
    onAttachmentsChange((current) => [...current, ...drafts]);
    await Promise.all(
      selected.map(async (file, index) => {
        const draft = drafts[index];
        if (!draft) {
          return;
        }
        try {
          const inputFile = renameClipboardImage(file, index);
          const preparedFile = await prepareCommentAttachmentFile(inputFile);
          let localPreviewUrl = draft.localPreviewUrl;
          if (preparedFile !== inputFile) {
            if (localPreviewUrl) {
              URL.revokeObjectURL(localPreviewUrl);
            }
            const nextLocalPreviewUrl = URL.createObjectURL(preparedFile);
            onAttachmentsChange((current) =>
              current.map((item) =>
                item.id === draft.id
                  ? {
                      ...item,
                      filename: preparedFile.name,
                      previewUrl: nextLocalPreviewUrl,
                      localPreviewUrl: nextLocalPreviewUrl,
                    }
                  : item,
              ),
            );
          }
          const attachment = await uploadFile(preparedFile);
          onAttachmentsChange((current) =>
            current.map((item) =>
              item.id === draft.id
                ? {
                    ...item,
                    attachment,
                    filename: attachment.originalFilename,
                    uploading: false,
                  }
                : item,
            ),
          );
        } catch (error) {
          onAttachmentsChange((current) =>
            current.map((item) =>
              item.id === draft.id
                ? {
                    ...item,
                    previewUrl: item.localPreviewUrl
                      ? item.previewUrl
                      : COMMENT_ATTACHMENT_ERROR_PREVIEW,
                    error: commentAttachmentErrorMessage(error),
                    uploading: false,
                  }
                : item,
            ),
          );
        }
      }),
    );
  }

  function handlePaste(event: ClipboardEvent<HTMLElement>) {
    if (event.defaultPrevented) {
      return;
    }
    const files = collectClipboardImageFiles(event.clipboardData);
    if (files.length === 0) {
      if (shouldReadAsyncClipboard(event.clipboardData)) {
        event.preventDefault();
        void addAsyncClipboardFiles();
      }
      return;
    }
    event.preventDefault();
    void addFiles(files);
  }

  async function addAsyncClipboardFiles() {
    try {
      const files = await collectAsyncClipboardImageFiles(navigator.clipboard);
      if (files.length === 0) {
        addAttachmentError(
          "浏览器没有把剪贴板图片暴露给网页，请使用 HTTPS/localhost，或点“添加图片”选择截图文件。",
        );
        return;
      }
      await addFiles(files);
    } catch (error) {
      addAttachmentError(commentAttachmentErrorMessage(error));
    }
  }

  function addAttachmentError(message: string) {
    if (disabled || attachmentsRef.current.length >= MAX_COMMENT_ATTACHMENTS) {
      return;
    }
    onAttachmentsChange((current) => {
      if (current.length >= MAX_COMMENT_ATTACHMENTS) {
        return current;
      }
      return [
        ...current,
        {
          id: crypto.randomUUID(),
          filename: "剪贴板图片",
          previewUrl: COMMENT_ATTACHMENT_ERROR_PREVIEW,
          uploading: false,
          error: message,
        },
      ];
    });
  }

  function handleDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setDragActive(false);
    void addFiles(Array.from(event.dataTransfer.files));
  }

  function handleFileInput(event: ChangeEvent<HTMLInputElement>) {
    const files = event.target.files;
    if (files?.length) {
      void addFiles(Array.from(files));
    }
    event.target.value = "";
  }

  function removeAttachment(id: string) {
    const removed = attachments.find((item) => item.id === id);
    if (removed?.localPreviewUrl) {
      URL.revokeObjectURL(removed.localPreviewUrl);
    }
    onAttachmentsChange((current) => current.filter((item) => item.id !== id));
  }

  const canAdd = !disabled && attachments.length < MAX_COMMENT_ATTACHMENTS;

  return (
    <div
      className={`comment-attachment-field${dragActive ? " is-drag-active" : ""}`}
      onDragEnter={(event) => {
        event.preventDefault();
        setDragActive(true);
      }}
      onDragOver={(event) => {
        event.preventDefault();
        event.dataTransfer.dropEffect = "copy";
      }}
      onDragLeave={() => setDragActive(false)}
      onDrop={handleDrop}
      onPasteCapture={handlePaste}
    >
      <span>{label}</span>
      <textarea
        value={value}
        maxLength={maxLength}
        placeholder={placeholder}
        disabled={disabled}
        onPaste={handlePaste}
        onChange={(event) => onChange(event.target.value)}
      />
      {attachments.length > 0 ? (
        <div className="comment-attachment-drafts">
          {attachments.map((item) => (
            <span
              className={`comment-attachment-draft${
                item.error ? " is-error" : ""
              }`}
              key={item.id}
            >
              <img src={item.previewUrl} alt="" />
              <em>
                {item.uploading
                  ? "上传中..."
                  : item.error
                    ? item.error
                    : item.filename}
              </em>
              <button
                type="button"
                aria-label={`移除 ${item.filename}`}
                onClick={() => removeAttachment(item.id)}
              >
                <X size={14} />
              </button>
            </span>
          ))}
        </div>
      ) : null}
      <div className="comment-attachment-toolbar">
        <input
          id={inputId}
          type="file"
          accept="image/png,image/jpeg,image/webp,image/gif"
          multiple
          disabled={!canAdd}
          onChange={handleFileInput}
        />
        <button
          type="button"
          disabled={!canAdd}
          onClick={() => document.getElementById(inputId)?.click()}
        >
          <ImagePlus size={16} />
          <span>添加图片</span>
        </button>
        <small>{attachments.length}/4</small>
      </div>
      {clipboardAccessRestricted ? (
        <p className="comment-attachment-clipboard-note">
          当前是普通 HTTP 访问，Chrome
          可能禁止读取微信截图剪贴板；截图粘贴无反应时，请改用 HTTPS
          或点“添加图片”选择截图文件。
        </p>
      ) : null}
    </div>
  );
}

function renameClipboardImage(file: File, index: number) {
  if (file.name && file.type) {
    return file;
  }
  const extension = imageExtension(file);
  const inferredType =
    file.type ||
    mimeTypeForSupportedExtension(fileExtension(file.name)) ||
    (file.name ? "" : mimeTypeForSupportedExtension(extension));
  return new File(
    [file],
    file.name || `clipboard-image-${index + 1}.${extension}`,
    {
      lastModified: file.lastModified,
      type: inferredType,
    },
  );
}

function imageExtension(file: File) {
  const filenameExtension = fileExtension(file.name);
  if (
    filenameExtension &&
    SUPPORTED_COMMENT_ATTACHMENT_EXTENSIONS.has(filenameExtension)
  ) {
    return filenameExtension === "jpeg" ? "jpg" : filenameExtension;
  }
  switch (file.type.toLowerCase()) {
    case "image/jpeg":
      return "jpg";
    case "image/webp":
      return "webp";
    case "image/gif":
      return "gif";
    default:
      return "png";
  }
}

function collectHTMLImageFiles(
  clipboardData: Partial<Pick<DataTransfer, "getData">>,
) {
  const html = clipboardData.getData?.("text/html") ?? "";
  if (!html.includes("data:image/")) {
    return [];
  }
  const files: File[] = [];
  const pattern =
    /src\s*=\s*["'](data:image\/(?:png|jpe?g|webp|gif);base64,[^"']+)["']/gi;
  for (const match of html.matchAll(pattern)) {
    const file = fileFromDataURL(match[1] ?? "", files.length);
    if (file) {
      files.push(file);
    }
  }
  return files;
}

function fileFromDataURL(dataURL: string, index: number) {
  const match =
    /^data:(image\/(?:png|jpe?g|webp|gif));base64,([a-z0-9+/=]+)$/i.exec(
      dataURL.trim(),
    );
  if (!match) {
    return null;
  }
  const mimeType = normalizeImageMime(match[1] ?? "");
  const base64 = match[2] ?? "";
  const estimatedBytes = Math.ceil((base64.length * 3) / 4);
  if (estimatedBytes > COMMENT_ATTACHMENT_PREPROCESS_LIMIT_BYTES) {
    return null;
  }
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return new File(
    [bytes],
    `clipboard-image-${index + 1}.${imageExtensionFromMime(mimeType)}`,
    {
      lastModified: Date.now(),
      type: mimeType,
    },
  );
}

function shouldReadAsyncClipboard(
  clipboardData: Partial<Pick<DataTransfer, "getData" | "types">>,
) {
  const types = Array.from(clipboardData.types ?? []).map((type) =>
    type.toLowerCase(),
  );
  const plainText = clipboardData.getData?.("text/plain") ?? "";
  const html = clipboardData.getData?.("text/html") ?? "";
  return (
    types.some(
      (type) =>
        type === "files" ||
        type.startsWith("image/") ||
        type === "com.apple.tiff" ||
        type === "public.tiff",
    ) ||
    html.includes("data:image/") ||
    (html.includes("<img") && plainText.trim() === "") ||
    (types.length === 0 && plainText.trim() === "")
  );
}

function isPotentialCommentImageFile(file: File, allowUnknown = false) {
  const type = file.type.toLowerCase();
  if (type.startsWith("image/")) {
    return true;
  }
  const extension = fileExtension(file.name);
  if (extension && POTENTIAL_COMMENT_ATTACHMENT_EXTENSIONS.has(extension)) {
    return true;
  }
  return allowUnknown && !type && !file.name;
}

function isSupportedCommentImageFile(file: File) {
  const type = file.type.toLowerCase();
  if (SUPPORTED_COMMENT_ATTACHMENT_MIMES.has(type)) {
    return true;
  }
  const extension = fileExtension(file.name);
  return Boolean(
    extension && SUPPORTED_COMMENT_ATTACHMENT_EXTENSIONS.has(extension),
  );
}

async function prepareCommentAttachmentFile(file: File): Promise<File> {
  if (!isSupportedCommentImageFile(file)) {
    throw new Error("仅支持 PNG、JPEG、WebP 或 GIF 图片");
  }
  if (file.size > COMMENT_ATTACHMENT_PREPROCESS_LIMIT_BYTES) {
    throw new Error("图片过大，请先压缩到 10 MB 以内再上传");
  }
  if (isGifFile(file)) {
    if (file.size > COMMENT_ATTACHMENT_MAX_UPLOAD_BYTES) {
      throw new Error("GIF 图片超过 10 MB，请压缩后再上传");
    }
    return file;
  }
  if (file.size <= COMMENT_ATTACHMENT_COMPRESS_THRESHOLD_BYTES) {
    return file;
  }

  let candidate = file;
  try {
    const compressed = await compressCommentImage(file);
    if (
      compressed.size < file.size ||
      file.size > COMMENT_ATTACHMENT_MAX_UPLOAD_BYTES
    ) {
      candidate = compressed;
    }
  } catch {
    if (file.size > COMMENT_ATTACHMENT_MAX_UPLOAD_BYTES) {
      throw new Error("图片无法在浏览器中压缩，请先压缩到 10 MB 以内");
    }
  }

  if (candidate.size > COMMENT_ATTACHMENT_MAX_UPLOAD_BYTES) {
    throw new Error("图片超过 10 MB，请压缩后再上传");
  }
  return candidate;
}

async function compressCommentImage(file: File): Promise<File> {
  const image = await loadCommentImage(file);
  try {
    const maxSide = Math.max(image.width, image.height);
    const scale =
      maxSide > COMMENT_ATTACHMENT_MAX_DIMENSION
        ? COMMENT_ATTACHMENT_MAX_DIMENSION / maxSide
        : 1;
    const width = Math.max(1, Math.round(image.width * scale));
    const height = Math.max(1, Math.round(image.height * scale));
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext("2d");
    if (!context) {
      throw new Error("canvas unavailable");
    }
    image.draw(context, width, height);
    const blob =
      (await canvasToBlob(canvas, "image/webp", 0.82, "image/webp")) ??
      (await canvasToBlob(canvas, "image/jpeg", 0.86));
    if (!blob) {
      throw new Error("canvas export failed");
    }
    const extension = imageExtensionFromMime(blob.type);
    return new File([blob], `${fileBaseName(file.name)}.${extension}`, {
      type: blob.type,
      lastModified: Date.now(),
    });
  } finally {
    image.close();
  }
}

type LoadedCommentImage = {
  width: number;
  height: number;
  draw: (
    context: CanvasRenderingContext2D,
    width: number,
    height: number,
  ) => void;
  close: () => void;
};

async function loadCommentImage(file: File): Promise<LoadedCommentImage> {
  if ("createImageBitmap" in window) {
    const bitmap = await createImageBitmap(file);
    return {
      width: bitmap.width,
      height: bitmap.height,
      draw: (context, width, height) => {
        context.drawImage(bitmap, 0, 0, width, height);
      },
      close: () => bitmap.close(),
    };
  }

  return new Promise((resolve, reject) => {
    const objectUrl = URL.createObjectURL(file);
    const image = new Image();
    image.onload = () => {
      resolve({
        width: image.naturalWidth,
        height: image.naturalHeight,
        draw: (context, width, height) => {
          context.drawImage(image, 0, 0, width, height);
        },
        close: () => URL.revokeObjectURL(objectUrl),
      });
    };
    image.onerror = () => {
      URL.revokeObjectURL(objectUrl);
      reject(new Error("image decode failed"));
    };
    image.src = objectUrl;
  });
}

function canvasToBlob(
  canvas: HTMLCanvasElement,
  mimeType: string,
  quality: number,
  expectedType?: string,
): Promise<Blob | null> {
  return new Promise((resolve) => {
    canvas.toBlob(
      (blob) => {
        if (!blob || (expectedType && blob.type !== expectedType)) {
          resolve(null);
          return;
        }
        resolve(blob);
      },
      mimeType,
      quality,
    );
  });
}

function commentAttachmentErrorMessage(error: unknown) {
  if (!(error instanceof Error)) {
    return "图片上传失败，请重试";
  }
  const message = error.message;
  if (message.includes("project upload storage is not configured")) {
    return "当前项目还没有配置上传存储，无法保存图片附件。";
  }
  if (message.includes("project upload storage is unavailable")) {
    return "当前项目上传存储不可用，请先检查项目存储设置。";
  }
  if (message.includes("unsupported image type")) {
    return "仅支持 PNG、JPEG、WebP 或 GIF 图片";
  }
  if (message.includes("file exceeds 10 MB")) {
    return "图片超过 10 MB，请压缩后再上传";
  }
  if (
    message.includes("clipboard image read unavailable") ||
    message.includes("NotAllowedError") ||
    message.includes("NotFoundError")
  ) {
    return "浏览器没有开放图片剪贴板读取。请使用 HTTPS/localhost，或点“添加图片”选择截图文件。";
  }
  return message;
}

function isGifFile(file: File) {
  return (
    file.type.toLowerCase() === "image/gif" ||
    fileExtension(file.name) === "gif"
  );
}

function fileExtension(filename: string) {
  const extension = filename.split(".").pop()?.toLowerCase();
  return extension && extension !== filename.toLowerCase() ? extension : "";
}

function fileBaseName(filename: string) {
  const trimmed = filename.trim();
  const base = trimmed ? trimmed.replace(/\.[^.]*$/, "") : "comment-image";
  return base || "comment-image";
}

function mimeTypeForSupportedExtension(extension: string) {
  switch (extension) {
    case "jpg":
    case "jpeg":
      return "image/jpeg";
    case "webp":
      return "image/webp";
    case "gif":
      return "image/gif";
    case "png":
      return "image/png";
    default:
      return "";
  }
}

function normalizeImageMime(mimeType: string) {
  const normalized = mimeType.toLowerCase();
  return normalized === "image/jpg" ? "image/jpeg" : normalized;
}

function imageExtensionFromMime(mimeType: string) {
  switch (normalizeImageMime(mimeType)) {
    case "image/jpeg":
      return "jpg";
    case "image/webp":
      return "webp";
    case "image/gif":
      return "gif";
    default:
      return "png";
  }
}

export function CommentAttachmentGallery({
  attachments,
}: {
  attachments: ReviewCommentAttachment[];
}) {
  const [preview, setPreview] = useState<ReviewCommentAttachment | null>(null);
  if (attachments.length === 0) {
    return null;
  }
  return (
    <>
      <div className="comment-attachment-gallery">
        {attachments.map((attachment) => (
          <button
            type="button"
            key={attachment.id}
            onClick={() => setPreview(attachment)}
          >
            <img src={attachment.url} alt={attachment.originalFilename} />
            <span>{attachment.originalFilename}</span>
          </button>
        ))}
      </div>
      {preview ? (
        <div
          className="comment-attachment-preview"
          role="dialog"
          aria-modal="true"
          aria-label={preview.originalFilename}
        >
          <button
            className="comment-attachment-preview-close"
            type="button"
            aria-label="关闭预览"
            onClick={() => setPreview(null)}
          >
            <X size={20} />
          </button>
          <img src={preview.url} alt={preview.originalFilename} />
        </div>
      ) : null}
    </>
  );
}
