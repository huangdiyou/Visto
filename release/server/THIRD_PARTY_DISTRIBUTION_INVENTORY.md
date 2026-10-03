# Visto Server Third-Party Distribution Inventory

This file summarizes the redistribution boundary of the Visto Server package.
The exact dependency set for a release is recorded in `THIRD_PARTY.spdx.json`.

## Server application

Visto Server includes open-source dependencies used by the Web application and
Go service. Their package names, versions and declared licenses are captured in
the SPDX inventory generated for each release. Required attribution text is
retained in `THIRD_PARTY_NOTICES.md`. The public source keeps Go module license
declarations in `scripts/server-go-license-inventory.json`; these are checked
against the license text included in the notices when generating the SBOM.

## Media runtime boundary

Native Visto Server packages do not bundle FFmpeg or ffprobe. A separately
distributed media runtime must include its own license, source location, build
record, checksums and third-party notices. A media runtime built with
non-redistributable options must not be shipped as part of Visto.

## Release requirement

A public package must include:

- `LICENSE.txt`
- `NOTICE.txt`
- `THIRD_PARTY_NOTICES.md`
- `THIRD_PARTY_DISTRIBUTION_INVENTORY.md`
- `THIRD_PARTY.spdx.json`

These files describe the software that is actually distributed; they are not a
substitute for the license files shipped by a separately installed media
runtime.
