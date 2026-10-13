#!/usr/bin/env python3
"""Build standalone website installers; no Server package rebuild is needed."""
from pathlib import Path
import argparse

ROOT = Path(__file__).resolve().parents[2]


def build(destination: Path) -> None:
    destination.mkdir(parents=True, exist_ok=True)
    source = ROOT / 'scripts/server-unix'
    template = (source / 'install-online.sh').read_text()
    marker = '# @VISTO_LEGACY_HELPERS@'
    assert template.count(marker) == 1
    embedded = []
    for name in ['setup-visto-server.sh', 'install-media-runtime.sh']:
        body = (source / name).read_text()
        delimiter = 'VISTO_EMBED_' + name.replace('-', '_').replace('.', '_').upper()
        assert delimiter not in body
        embedded.append(f'cat > "$STAGED/scripts/{name}" <<\'{delimiter}\'\n{body.rstrip()}\n{delimiter}')
    # Legacy 1.0.3 env files predate the shared public-payload permission guard.
    # Append only that helper, generated from the canonical source.
    env = (source / 'visto-server-env.sh').read_text()
    helper = env[env.index('visto_unix_public_payload() {'):]
    embedded.append("if ! grep -q '^visto_unix_public_payload()' \"$STAGED/scripts/visto-server-env.sh\"; then\n"
                    "cat >> \"$STAGED/scripts/visto-server-env.sh\" <<'VISTO_PUBLIC_PAYLOAD_HELPER'\n"
                    + helper + "VISTO_PUBLIC_PAYLOAD_HELPER\nfi")
    artifact = template.replace(marker, '\n'.join(embedded))
    (destination / 'install.sh').write_text(artifact)
    (destination / 'install.sh').chmod(0o755)
    windows = ROOT / 'scripts/server-win/Install-VistoOnline.ps1'
    if windows.exists():
        # Windows PowerShell 5.1 needs BOM for Chinese status text.
        (destination / 'install.ps1').write_text(windows.read_text(encoding='utf-8-sig'), encoding='utf-8-sig')
    print(f'Website installers: {destination}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=ROOT / 'dist/website-installers')
    build(parser.parse_args().output)
