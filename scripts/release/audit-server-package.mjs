// Portable Visto Server package audit.
//
// Entry point shared by the native packaging recipes (currently
// scripts/build-server-macos.sh; the Windows recipe keeps its own
// PowerShell audit for the Windows-only checks). It answers one question:
// does this archive satisfy the native package contract used by the installer?
//
// The contract's path whitelist has exactly one implementation:
// scripts/server-unix/visto-server-env.sh. This tool reads the two variables
// out of that file instead of restating them, so a change to the contract
// cannot silently diverge from the packaging gate.
//
// The archive's own member list and hash are produced by the caller with the
// platform's native tools (tar / shasum), and passed in. That keeps this tool
// free of archive-format parsing, which differs per platform.
//
// Usage:
//   node scripts/release/audit-server-package.mjs \
//     --archive dist/server/Visto-Server_1.0.0-rc.1_macos-arm64.tar.gz \
//     --platform macos-arm64 \
//     --version 1.0.0-rc.1 \
//     --entries <newline separated member names> \
//     --listing <tar -tvzf output> \
//     --sha256 <hex> \
//     [--sha256-file <sidecar>] \
//     [--env-file scripts/server-unix/visto-server-env.sh] \
//     [--bundled-runtime] \
//     [--skip-executable-mode-check] \
//     [--report <out.json>] \
//     [--json]
//
// Exit code 0 only when every check passes.

import crypto from "node:crypto";
import fs from "node:fs";
import { execFileSync } from "node:child_process";
import path from "node:path";

// Read declared members from the archive itself, without extracting files to disk.
// Canonical license/notices cannot be replaced by empty or placeholder files.
export function verifyDeclarationContents(archive, entries) {
  const problems = [];
  for (const [name, relative] of Object.entries({
    "LICENSE.txt": "../../release/server/LICENSE.txt",
    "NOTICE.txt": "../../release/server/NOTICE.txt",
    "THIRD_PARTY_NOTICES.md": "../../release/server/THIRD_PARTY_NOTICES.md",
  })) {
    try {
      const member = entries.find((entry) => normaliseEntry(entry) === name);
      if (!member) throw new Error("missing archive member");
      const actual = execFileSync("tar", ["-xOf", archive, member], {maxBuffer: 4 * 1024 * 1024, encoding: "utf8"});
      const expected = fs.readFileSync(new URL(relative, import.meta.url), "utf8");
      const normalize = (value) => value.replaceAll("\r\n", "\n").trim();
      if (normalize(actual) !== normalize(expected)) throw new Error("does not match scoped release material");
    } catch (error) { problems.push(`${name}: ${error.message}`); }
  }
  try {
    const member = entries.find((entry) => normaliseEntry(entry) === "THIRD_PARTY.spdx.json");
    if (!member) throw new Error("missing archive member");
    const sbom = JSON.parse(execFileSync("tar", ["-xOf", archive, member], {maxBuffer: 16 * 1024 * 1024, encoding: "utf8"}));
    if (!sbom.spdxVersion?.startsWith("SPDX-") || !Array.isArray(sbom.packages) || sbom.packages.length < 2 ||
      !sbom.packages.some((entry) => entry.name === "react") ||
      !sbom.packages.some((entry) => entry.name === "modernc.org/sqlite")) throw new Error("missing actual Server dependency inventory");
  } catch (error) { problems.push(`THIRD_PARTY.spdx.json: ${error.message}`); }
  return problems;
}

/** Declaration files every Server package must ship (contract section 2.2). */
export const REQUIRED_DECLARATION_FILES = [
  "LICENSE.txt",
  "NOTICE.txt",
  "THIRD_PARTY_NOTICES.md",
  "THIRD_PARTY.spdx.json",
];

/** Extra declarations required when the package bundles a media runtime. */
export const REQUIRED_BUNDLED_RUNTIME_DECLARATIONS = [
  "FFMPEG_DISTRIBUTION.md",
  "THIRD_PARTY_DISTRIBUTION_INVENTORY.md",
];

/** Binaries and Studio entry point required on every native platform. */
export const REQUIRED_MEMBERS = {
  unix: ["bin/visto-server", "bin/visto-core", "web/index.html"],
  windows: ["bin/visto-server.exe", "bin/visto-core.exe", "web/index.html"],
};

/**
 * Prefixes whose regular files must keep the executable bit in a tar.gz.
 *
 * Contract section 2.2 requires bin/ and scripts/ at 0755 and everything else
 * at 0644. Nothing else in the pipeline asserted this: the installer's
 * `visto_unix_validate_package` checks member types and paths but not modes,
 * so a package could lose its executable bit and still install.
 */
export const EXECUTABLE_PREFIXES = ["bin/", "scripts/"];

export class PackageAuditError extends Error {
  constructor(problems) {
    super(`server package audit failed:\n  - ${problems.join("\n  - ")}`);
    this.name = "PackageAuditError";
    this.problems = problems;
  }
}

/**
 * Parses `tar -tvzf` output.
 *
 * The member name is everything after the fifth field, which is the layout both
 * GNU tar and bsdtar emit (mode, owner, size, date, time, name). Member types
 * come back as the first character of the mode field.
 */
export function parseVerboseListing(listingText) {
  const members = [];
  for (const line of String(listingText).split(/\r?\n/)) {
    if (line.trim() === "") {
      continue;
    }
    const match = /^(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(.+)$/.exec(line);
    if (!match) {
      members.push({ raw: line, mode: "", type: "?", name: "", parseable: false });
      continue;
    }
    members.push({
      mode: match[1],
      type: match[1].slice(0, 1),
      name: normaliseEntry(match[6]),
      parseable: true,
      raw: line,
    });
  }
  return members;
}

export function normaliseEntry(name) {
  let value = String(name ?? "").replaceAll("\\", "/");
  while (value.startsWith("./")) {
    value = value.slice(2);
  }
  return value;
}

// Reads the contract's whitelist from its single implementation instead of
// duplicating the values here. The two variables are plain bash assignments
// with double-quoted or newline-separated string literals.
export function readContractWhitelist(envFileSource) {
  const prefixesMatch = /VISTO_UNIX_ALLOWED_PREFIXES="([^"]*)"/.exec(envFileSource);
  const rootFilesMatch = /VISTO_UNIX_ALLOWED_ROOT_FILES="([^"]*)"/.exec(envFileSource);
  if (!prefixesMatch || !rootFilesMatch) {
    throw new PackageAuditError([
      "scripts/server-unix/visto-server-env.sh no longer declares VISTO_UNIX_ALLOWED_PREFIXES and " +
        "VISTO_UNIX_ALLOWED_ROOT_FILES; the package whitelist has one implementation and this gate must read it",
    ]);
  }
  return {
    prefixes: prefixesMatch[1].split(/\s+/).filter(Boolean),
    rootFiles: rootFilesMatch[1].split(/\s+/).filter(Boolean),
  };
}

/**
 * Audits a package from its member list.
 *
 * @param {object} input
 * @param {string} input.platform frozen platform id
 * @param {string} input.version candidate version
 * @param {string[]} input.entries archive member names
 * @param {string} input.sha256 archive SHA-256, lower-case hex
 * @param {string} [input.sha256FileContent] contents of the .sha256 sidecar
 * @param {{prefixes: string[], rootFiles: string[]}} [input.whitelist]
 * @param {boolean} [input.bundledRuntime]
 */
export function auditPackage({
  platform,
  version,
  entries,
  sha256,
  sha256FileContent = "",
  whitelist = null,
  bundledRuntime = false,
  listing = null,
  requireExecutableModes = true,
}) {
  const problems = [];
  const normalised = entries.map(normaliseEntry).filter((entry) => entry !== "");

  if (normalised.length === 0) {
    problems.push("the archive has no members");
  }

  const seen = new Set();
  const duplicates = [];
  for (const entry of normalised) {
    if (seen.has(entry)) {
      duplicates.push(entry);
    }
    seen.add(entry);
  }
  if (duplicates.length > 0) {
    problems.push(`duplicate archive paths: ${[...new Set(duplicates)].join(", ")}`);
  }

  const unsafe = [];
  for (const entry of normalised) {
    if (entry.startsWith("/")) {
      unsafe.push(`${entry} (absolute path)`);
      continue;
    }
    if (entry.includes(":")) {
      unsafe.push(`${entry} (contains a drive or stream separator)`);
      continue;
    }
    if (entry.split("/").includes("..")) {
      unsafe.push(`${entry} (parent traversal)`);
    }
  }
  if (unsafe.length > 0) {
    problems.push(`unsafe archive members: ${unsafe.join(", ")}`);
  }

  const family = platform.startsWith("windows-") ? "windows" : "unix";
  const requiredMembers = REQUIRED_MEMBERS[family];

  const requiredDeclarations = [...REQUIRED_DECLARATION_FILES];
  if (bundledRuntime) {
    requiredDeclarations.push(...REQUIRED_BUNDLED_RUNTIME_DECLARATIONS);
  }

  const missingMembers = requiredMembers.filter((entry) => !seen.has(entry));
  const missingDeclarations = requiredDeclarations.filter((entry) => !seen.has(entry));
  if (missingMembers.length > 0) {
    problems.push(`missing required members: ${missingMembers.join(", ")}`);
  }
  if (missingDeclarations.length > 0) {
    problems.push(
      `missing required declaration files: ${missingDeclarations.join(", ")} ` +
        `(contract section 2.2; these must ship real material, never a generated placeholder)`,
    );
  }

  const whitelistViolations = [];
  if (whitelist) {
    for (const entry of normalised) {
      const isDirectory = entry.endsWith("/");
      const normalisedEntry = isDirectory ? entry.slice(0, -1) : entry;
      if (normalisedEntry === "") {
        continue;
      }
      const candidate = isDirectory ? `${normalisedEntry}/` : normalisedEntry;
      if (whitelist.prefixes.some((prefix) => candidate.startsWith(prefix))) {
        continue;
      }
      if (!candidate.includes("/") && whitelist.rootFiles.includes(candidate)) {
        continue;
      }
      whitelistViolations.push(candidate);
    }
    if (whitelistViolations.length > 0) {
      problems.push(`paths outside the frozen package whitelist: ${whitelistViolations.join(", ")}`);
    }
  }

  const declaredHashes = [...String(sha256FileContent).matchAll(/\b[0-9a-fA-F]{64}\b/g)].map((match) =>
    match[0].toLowerCase(),
  );
  const sha256FileMatches = declaredHashes.length > 0 && declaredHashes.includes(sha256);

  // Member types and the executable bit. A zip has no POSIX modes, so this only
  // applies to the tar.gz platforms.
  let executableModeCheck = family === "unix" ? "not-supplied" : "not-applicable";
  const nonExecutableMembers = [];
  const unsupportedMemberTypes = [];
  if (family === "unix" && requireExecutableModes) {
    if (!listing) {
      problems.push(
        "no verbose tar listing was supplied, so member types and the executable bit could not be checked",
      );
    } else {
      executableModeCheck = "enforced";
      for (const member of listing) {
        if (!member.parseable) {
          problems.push(`unparsable tar listing line: ${member.raw}`);
          continue;
        }
        if (member.type !== "-" && member.type !== "d") {
          unsupportedMemberTypes.push(`${member.name || member.raw} (type "${member.type}")`);
          continue;
        }
        if (member.type === "d") {
          continue;
        }
        const ownerExecutable = member.mode.slice(3, 4) === "x";
        if (EXECUTABLE_PREFIXES.some((prefix) => member.name.startsWith(prefix)) && !ownerExecutable) {
          nonExecutableMembers.push(member.name);
        }
      }
      if (unsupportedMemberTypes.length > 0) {
        problems.push(
          `unsupported archive member types (contract section 2.2 allows plain files and directories only): ${unsupportedMemberTypes.join(", ")}`,
        );
      }
      if (nonExecutableMembers.length > 0) {
        problems.push(
          `members under ${EXECUTABLE_PREFIXES.join(" ")} lost the executable bit (contract section 2.2 requires 0755): ${nonExecutableMembers.join(", ")}`,
        );
      }
    }
  } else if (family === "unix" && !requireExecutableModes) {
    executableModeCheck = "skipped";
  }

  const passed = problems.length === 0 && sha256FileMatches;

  return {
    schema: "visto-server-package-audit/v1",
    platform,
    version,
    archive: null,
    sha256,
    entryCount: normalised.length,
    requiredMembers,
    requiredDeclarationFiles: requiredDeclarations,
    missingRequiredEntries: [...missingMembers, ...missingDeclarations],
    duplicateEntries: [...new Set(duplicates)],
    unsafeEntries: unsafe,
    whitelistViolations,
    executableModeCheck,
    nonExecutableMembers,
    unsupportedMemberTypes,
    sha256FileMatches,
    bundledRuntime,
    problems,
    passed,
    checkedAt: new Date().toISOString(),
  };
}

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

function parseArgs(argv) {
  const parsed = { _: [] };
  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index];
    if (token.startsWith("--")) {
      const key = token.slice(2);
      const value = argv[index + 1] !== undefined && !argv[index + 1].startsWith("--") ? argv[++index] : true;
      parsed[key] = value;
    } else {
      parsed._.push(token);
    }
  }
  return parsed;
}

export function sha256File(filePath) {
  return crypto.createHash("sha256").update(fs.readFileSync(filePath)).digest("hex");
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const missing = [];
  for (const required of ["archive", "platform", "version", "entries", "sha256"]) {
    if (typeof args[required] !== "string") {
      missing.push(`--${required}`);
    }
  }
  if (missing.length > 0) {
    console.error(`usage: audit-server-package.mjs ${missing.join(" ")} ...`);
    process.exitCode = 2;
    return;
  }

  if (!fs.existsSync(args.archive)) {
    console.error(`archive not found: ${args.archive}`);
    process.exitCode = 2;
    return;
  }

  const entries = fs
    .readFileSync(args.entries, "utf8")
    .split(/\r?\n/)
    .filter((line) => line.trim() !== "");
  const sha256 = String(args.sha256).trim().toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(sha256)) {
    console.error("--sha256 must be a 64-character hex digest");
    process.exitCode = 2;
    return;
  }
  const actualSha256 = sha256File(args.archive);
  const sha256MatchesArchive = actualSha256 === sha256;

  let sha256FileContent = "";
  if (typeof args["sha256-file"] === "string") {
    if (!fs.existsSync(args["sha256-file"])) {
      console.error(`sha256 sidecar not found: ${args["sha256-file"]}`);
      process.exitCode = 2;
      return;
    }
    sha256FileContent = fs.readFileSync(args["sha256-file"], "utf8");
  }

  let whitelist = null;
  if (typeof args["env-file"] === "string") {
    whitelist = readContractWhitelist(fs.readFileSync(args["env-file"], "utf8"));
  }

  let listing = null;
  if (typeof args.listing === "string") {
    if (!fs.existsSync(args.listing)) {
      console.error(`tar listing not found: ${args.listing}`);
      process.exitCode = 2;
      return;
    }
    listing = parseVerboseListing(fs.readFileSync(args.listing, "utf8"));
  }

  const requireExecutableModes = args["skip-executable-mode-check"] !== true;
  if (!requireExecutableModes) {
    console.warn(
      "WARNING: --skip-executable-mode-check disables the executable-bit check. The report records " +
        "executableModeCheck=skipped, this audit is not valid release evidence, and a non-POSIX host " +
        "cannot express POSIX modes (the macOS/Linux recipes use chmod and never pass this flag).",
    );
  }

  const report = auditPackage({
    platform: args.platform,
    version: args.version,
    entries,
    sha256,
    sha256FileContent,
    whitelist,
    bundledRuntime: args["bundled-runtime"] === true,
    listing,
    requireExecutableModes,
  });
  const contentProblems = verifyDeclarationContents(args.archive, entries);
  report.declarationContentsVerified = contentProblems.length === 0;
  report.problems.push(...contentProblems);
  if (contentProblems.length) report.passed = false;
  report.archive = path.basename(args.archive);
  report.sha256MatchesArchive = sha256MatchesArchive;
  if (!sha256MatchesArchive) {
    report.problems.push(`declared sha256 ${sha256} does not hash the archive (${actualSha256})`);
    report.passed = false;
  }

  const reportPath = typeof args.report === "string" ? args.report : `${args.archive}.audit.json`;
  fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`server package audit report: ${reportPath}`);

  if (args.json) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    console.log(
      `audit ${report.passed ? "PASSED" : "FAILED"} — ${args.platform} ${args.version}, ` +
        `${report.entryCount} entries, ${report.missingRequiredEntries.length} missing required, ` +
        `${report.whitelistViolations.length} outside the whitelist`,
    );
    for (const problem of report.problems) {
      console.log(`  - ${problem}`);
    }
  }

  if (!report.passed) {
    process.exitCode = 1;
  }
}

if (import.meta.url === (await import("node:url")).pathToFileURL(process.argv[1] ?? "").href) {
  main();
}
