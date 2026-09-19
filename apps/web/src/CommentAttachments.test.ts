import { describe, expect, it } from "vitest";
import {
  collectAsyncClipboardImageFiles,
  collectClipboardImageFiles,
  isClipboardImageReadRestricted,
} from "./CommentAttachments";

describe("comment attachment clipboard helpers", () => {
  it("collects pasted screenshots from clipboard items", () => {
    const screenshot = new File(["image"], "", { type: "image/png" });
    const textFile = new File(["note"], "note.txt", { type: "text/plain" });
    const clipboardData = {
      files: [textFile],
      items: [
        {
          kind: "file",
          type: "text/plain",
          getAsFile: () => textFile,
        },
        {
          kind: "file",
          type: "image/png",
          getAsFile: () => screenshot,
        },
      ],
    } as unknown as DataTransfer;

    const files = collectClipboardImageFiles(clipboardData);

    expect(files).toHaveLength(1);
    expect(files[0]?.name).toBe("clipboard-image-1.png");
    expect(files[0]?.type).toBe("image/png");
  });

  it("falls back to clipboard files when the browser does not expose items", () => {
    const image = new File(["image"], "capture.webp", { type: "image/webp" });
    const clipboardData = {
      files: [image],
      items: [],
    } as unknown as DataTransfer;

    expect(collectClipboardImageFiles(clipboardData)).toEqual([image]);
  });

  it("keeps mac clipboard files when item mime is empty", () => {
    const image = new File(["image"], "Screen Shot.png", { type: "" });
    const clipboardData = {
      files: [],
      items: [
        {
          kind: "file",
          type: "",
          getAsFile: () => image,
        },
      ],
    } as unknown as DataTransfer;

    const files = collectClipboardImageFiles(clipboardData);

    expect(files).toHaveLength(1);
    expect(files[0]?.name).toBe("Screen Shot.png");
    expect(files[0]?.type).toBe("image/png");
  });

  it("keeps unsupported image-like clipboard files so the UI can explain them", () => {
    const image = new File(["image"], "photo.heic", { type: "" });
    const clipboardData = {
      files: [],
      items: [
        {
          kind: "file",
          type: "",
          getAsFile: () => image,
        },
      ],
    } as unknown as DataTransfer;

    const files = collectClipboardImageFiles(clipboardData);

    expect(files).toHaveLength(1);
    expect(files[0]?.name).toBe("photo.heic");
    expect(files[0]?.type).toBe("");
  });

  it("collects pasted html data-url images", () => {
    const clipboardData = {
      files: [],
      items: [],
      types: ["text/html"],
      getData: (type: string) =>
        type === "text/html"
          ? '<img src="data:image/png;base64,aW1hZ2U=">'
          : "",
    } as unknown as DataTransfer;

    const files = collectClipboardImageFiles(clipboardData);

    expect(files).toHaveLength(1);
    expect(files[0]?.name).toBe("clipboard-image-1.png");
    expect(files[0]?.type).toBe("image/png");
    expect(files[0]?.size).toBeGreaterThan(0);
  });

  it("collects async clipboard image items", async () => {
    const clipboard = {
      read: async () => [
        {
          types: ["text/plain"],
          getType: async () => new Blob(["text"], { type: "text/plain" }),
        },
        {
          types: ["image/png"],
          getType: async () => new Blob(["image"], { type: "image/png" }),
        },
      ],
    } as unknown as Pick<Clipboard, "read">;

    const files = await collectAsyncClipboardImageFiles(clipboard);

    expect(files).toHaveLength(1);
    expect(files[0]?.name).toBe("clipboard-image-1.png");
    expect(files[0]?.type).toBe("image/png");
  });

  it("marks public http ip origins as restricted for async image clipboard reads", () => {
    // 203.0.113.50 is an RFC 5737 documentation address. Never use a real
    // deployment host here: this file ships in the public Server snapshot.
    expect(
      isClipboardImageReadRestricted({ hostname: "203.0.113.50" }, false),
    ).toBe(true);
    expect(
      isClipboardImageReadRestricted({ hostname: "127.0.0.1" }, false),
    ).toBe(false);
    expect(
      isClipboardImageReadRestricted({ hostname: "example.com" }, true),
    ).toBe(false);
  });
});
