import { afterEach, describe, expect, it, vi } from "vitest";
import { copyText } from "./clipboard";

// The web suite runs in a Node environment, so the two surfaces copyText uses
// are stubbed here. Both branches matter: a self-hosted deployment on plain
// HTTP has no async Clipboard API and only the textarea fallback. Node exposes
// `navigator` as a getter-only global, so stubs are installed with
// defineProperty and the original descriptors are restored afterwards.
const host = globalThis as Record<string, unknown>;
const originalNavigator = Object.getOwnPropertyDescriptor(host, "navigator");
const originalDocument = Object.getOwnPropertyDescriptor(host, "document");

function defineGlobal(name: "navigator" | "document", value: unknown) {
  Object.defineProperty(host, name, {
    value,
    configurable: true,
    writable: true,
  });
}

function restoreGlobal(
  name: "navigator" | "document",
  descriptor: PropertyDescriptor | undefined,
) {
  if (descriptor) {
    Object.defineProperty(host, name, descriptor);
    return;
  }
  delete host[name];
}

interface ClipboardStub {
  written: string[];
  textareas: { value: string; selected: boolean }[];
  attached: number;
  execCommandCalls: string[];
}

function install(options: {
  clipboard?: "available" | "blocked" | "absent";
  execCommand?: boolean | "throws";
}): ClipboardStub {
  const stub: ClipboardStub = {
    written: [],
    textareas: [],
    attached: 0,
    execCommandCalls: [],
  };
  const writeText = vi.fn(async (value: string) => {
    if (options.clipboard === "blocked") {
      throw new Error("NotAllowedError: Document is not focused.");
    }
    stub.written.push(value);
  });
  defineGlobal(
    "navigator",
    options.clipboard === "absent" ? {} : { clipboard: { writeText } },
  );
  defineGlobal("document", {
    createElement: () => {
      const textarea = {
        value: "",
        selected: false,
        style: {} as Record<string, string>,
        setAttribute: () => {},
        select: () => {
          textarea.selected = true;
        },
      };
      stub.textareas.push(textarea);
      return textarea;
    },
    body: {
      appendChild: () => {
        stub.attached += 1;
      },
      removeChild: () => {
        stub.attached -= 1;
      },
    },
    execCommand: (command: string) => {
      stub.execCommandCalls.push(command);
      if (options.execCommand === "throws") {
        throw new Error("execCommand is not supported");
      }
      return options.execCommand === true;
    },
  });
  return stub;
}

afterEach(() => {
  restoreGlobal("navigator", originalNavigator);
  restoreGlobal("document", originalDocument);
});

describe("copyText", () => {
  it("uses the async clipboard API when the origin exposes it", async () => {
    const stub = install({ clipboard: "available" });
    await expect(
      copyText("sudo /opt/visto/current/scripts/x.sh"),
    ).resolves.toBe(true);
    expect(stub.written).toEqual(["sudo /opt/visto/current/scripts/x.sh"]);
    expect(stub.textareas).toEqual([]);
  });

  it("falls back to a hidden textarea when the async API is blocked", async () => {
    const stub = install({ clipboard: "blocked", execCommand: true });
    await expect(copyText("docker compose up -d")).resolves.toBe(true);
    expect(stub.execCommandCalls).toEqual(["copy"]);
    expect(stub.textareas).toHaveLength(1);
    expect(stub.textareas[0]?.value).toBe("docker compose up -d");
    expect(stub.textareas[0]?.selected).toBe(true);
    expect(stub.attached).toBe(0);
  });

  it("reports failure when the fallback cannot copy", async () => {
    const stub = install({ clipboard: "absent", execCommand: false });
    await expect(copyText("value")).resolves.toBe(false);
    expect(stub.attached).toBe(0);
  });

  it("reports failure when execCommand throws", async () => {
    const stub = install({ clipboard: "absent", execCommand: "throws" });
    await expect(copyText("value")).resolves.toBe(false);
    expect(stub.attached).toBe(0);
  });

  it("reports failure when there is no document to fall back to", async () => {
    defineGlobal("navigator", {});
    defineGlobal("document", undefined);
    await expect(copyText("value")).resolves.toBe(false);
  });
});
