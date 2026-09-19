import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const [outputPath] = process.argv.slice(2);

if (!outputPath) {
  throw new Error(
    "Usage: node scripts/generate-server-sbom.mjs <output-spdx-json>",
  );
}

const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const packageLock = JSON.parse(
  await readFile(path.join(repositoryRoot, "package-lock.json"), "utf8"),
);

function spdxId(prefix, name) {
  return `SPDXRef-${prefix}-${name.replaceAll(/[^A-Za-z0-9.-]/g, "-")}`;
}

function npmPackages() {
  return Object.entries(packageLock.packages)
    .filter(([packagePath, value]) => {
      if (
        !packagePath ||
        packagePath.startsWith("apps/desktop") ||
        packagePath.includes("/desktop/")
      ) {
        return false;
      }

      return (
        packagePath.startsWith("node_modules/") && !value.link && value.version
      );
    })
    .map(([packagePath, value]) => {
      const name = packagePath.slice("node_modules/".length);
      return {
        SPDXID: spdxId("npm", name),
        name,
        versionInfo: value.version,
        downloadLocation: value.resolved ?? "NOASSERTION",
        licenseConcluded: value.license ?? "NOASSERTION",
        licenseDeclared: value.license ?? "NOASSERTION",
        externalRefs: [
          {
            referenceCategory: "PACKAGE-MANAGER",
            referenceType: "purl",
            referenceLocator: `pkg:npm/${encodeURIComponent(name)}@${value.version}`,
          },
        ],
      };
    });
}

async function goModules() {
  const goSum = await readFile(
    path.join(repositoryRoot, "services", "core", "go.sum"),
    "utf8",
  );
  const modules = new Map();

  for (const line of goSum.split(/\r?\n/)) {
    const [name, version] = line.split(" ");
    if (!name || !version || version.endsWith("/go.mod")) {
      continue;
    }

    modules.set(`${name}@${version}`, { name, version });
  }

  return [...modules.values()].map((module) => ({
    SPDXID: spdxId("go", module.name),
    name: module.name,
    versionInfo: module.version,
    downloadLocation: module.name.startsWith("golang.org/")
      ? `https://${module.name}`
      : `https://pkg.go.dev/${module.name}`,
    licenseConcluded: "NOASSERTION",
    licenseDeclared: "NOASSERTION",
    externalRefs: [
      {
        referenceCategory: "PACKAGE-MANAGER",
        referenceType: "purl",
        referenceLocator: `pkg:golang/${module.name}@${module.version}`,
      },
    ],
  }));
}

const documentNamespace = `https://visto.example/sbom/${new Date().toISOString().replaceAll(/[:.]/g, "-")}`;
const documentDescribes = "SPDXRef-Visto-Server";
const packages = [
  {
    SPDXID: documentDescribes,
    name: "Visto Server",
    versionInfo: "NOASSERTION",
    downloadLocation: "NOASSERTION",
    licenseConcluded: "Apache-2.0",
    licenseDeclared: "Apache-2.0",
    primaryPackagePurpose: "APPLICATION",
  },
  ...npmPackages(),
  ...(await goModules()),
];

const sbom = {
  spdxVersion: "SPDX-2.3",
  dataLicense: "CC0-1.0",
  SPDXID: "SPDXRef-DOCUMENT",
  name: "Visto Server dependency inventory",
  documentNamespace,
  creationInfo: {
    created: new Date().toISOString(),
    creators: ["Tool: Visto generate-server-sbom.mjs"],
  },
  packages,
  relationships: packages.slice(1).map((item) => ({
    spdxElementId: documentDescribes,
    relationshipType: "DEPENDS_ON",
    relatedSpdxElement: item.SPDXID,
  })),
};

await mkdir(path.dirname(path.resolve(outputPath)), { recursive: true });
await writeFile(outputPath, `${JSON.stringify(sbom, null, 2)}\n`, "utf8");
console.log(`Server SPDX dependency inventory written: ${outputPath}`);
