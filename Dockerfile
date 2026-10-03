# syntax=docker/dockerfile:1

FROM node:22-bookworm-slim@sha256:d649c27dae7ba0137b3cef5dd75baa422c08dc3d9e3fc0c23dfb172dc3cc6436 AS web-build
WORKDIR /workspace
COPY package.json package-lock.json ./
COPY packages ./packages
COPY apps/web/package.json ./apps/web/package.json
RUN npm ci --ignore-scripts
COPY apps/web ./apps/web
RUN npm run build --workspace @review-studio/web

FROM golang:1.26-bookworm@sha256:6ef6e30f0ea5c384f6d111cf856e024e3086bbdcb1779da3f3b3fbba0aea53d2 AS core-build
ARG VISTO_VERSION=1.0.0-dev
WORKDIR /workspace
COPY go.work ./
COPY services/core/go.mod services/core/go.sum ./services/core/
RUN cd services/core && go mod download
COPY services/core ./services/core
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X main.version=${VISTO_VERSION}" -o /out/visto-core ./services/core/cmd/server \
  && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X main.version=${VISTO_VERSION}" -o /out/visto-server ./services/core/cmd/visto-server

# An optional named BuildKit context supplies only preverified runtime archives.
# The default is empty of archives; normal builds retain the HTTPS download path.
FROM scratch AS runtime-input
COPY scripts/server-ffmpeg-runtime.json /input-marker.json

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS ffmpeg-runtime
ARG TARGETARCH
WORKDIR /runtime
COPY scripts/server-ffmpeg-runtime.json /runtime/server-ffmpeg-runtime.json
COPY --from=runtime-input / /runtime/input/
RUN apt-get update \
  && apt-get install --yes --no-install-recommends ca-certificates curl jq xz-utils \
  && rm -rf /var/lib/apt/lists/* \
  && case "$TARGETARCH" in amd64) asset="linux-amd64" ;; arm64) asset="linux-arm64" ;; *) echo "Unsupported FFmpeg runtime architecture: $TARGETARCH" >&2; exit 1 ;; esac \
  && url="$(jq -r --arg asset "$asset" '.assets[$asset].url' server-ffmpeg-runtime.json)" \
  && expected_sha="$(jq -r --arg asset "$asset" '.assets[$asset].sha256' server-ffmpeg-runtime.json)" \
  && archive_name="${url##*/}" \
  && if test -f "/runtime/input/$archive_name"; then \
       cp "/runtime/input/$archive_name" ffmpeg.tar.xz; \
     else \
       curl --fail --location --proto '=https' --tlsv1.2 "$url" --output ffmpeg.tar.xz; \
     fi \
  && echo "$expected_sha  ffmpeg.tar.xz" | sha256sum --check --strict \
  && mkdir extracted out \
  && tar -xJf ffmpeg.tar.xz -C extracted \
  && ffmpeg_path="$(find extracted -type f -name ffmpeg -print -quit)" \
  && ffprobe_path="$(find extracted -type f -name ffprobe -print -quit)" \
  && license_path="$(find extracted -type f \( -iname LICENSE -o -iname LICENSE.txt -o -iname COPYING -o -iname COPYING.txt \) -print -quit)" \
  && test -n "$ffmpeg_path" && test -n "$ffprobe_path" && test -n "$license_path" \
  && install -m 0755 "$ffmpeg_path" out/ffmpeg \
  && install -m 0755 "$ffprobe_path" out/ffprobe \
  && install -m 0644 "$license_path" out/LICENSE.txt \
  && cp server-ffmpeg-runtime.json out/server-ffmpeg-runtime.json \
  && test -f extracted/SOURCE.txt && test -f extracted/THIRD_PARTY_NOTICES.md && test -f extracted/THIRD_PARTY.spdx.json && test -d extracted/licenses \
  && cp -a extracted/licenses out/licenses \
  && cp extracted/SOURCE.txt extracted/THIRD_PARTY_NOTICES.md extracted/THIRD_PARTY.spdx.json out/ \
  && ffmpeg_version="$(out/ffmpeg -version | sed -n '1p')" \
  && ffmpeg_configure="$(out/ffmpeg -version | sed -n 's/^configuration: //p')" \
  && printf 'Source: %s\nVersion: %s\nConfigure: %s\nSHA-256: archive=%s; ffmpeg=%s; ffprobe=%s\nLicense: %s\nSoftware video encoder: libopenh264\n' \
    "$url" "$ffmpeg_version" "$ffmpeg_configure" "$expected_sha" "$(sha256sum out/ffmpeg | cut -d ' ' -f 1)" "$(sha256sum out/ffprobe | cut -d ' ' -f 1)" "$(jq -r '.license' server-ffmpeg-runtime.json)" > out/FFmpeg-BUILD.txt \
  && out/ffmpeg -L | grep -q 'Lesser General Public' \
  && ! out/ffmpeg -version | grep -q -- '--enable-gpl' \
  && ! out/ffmpeg -version | grep -q -- '--enable-nonfree'

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS core
RUN apt-get update \
  && apt-get install --yes --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd --system visto \
  && useradd --system --gid visto --home-dir /var/lib/visto --create-home visto \
  && mkdir -p /var/lib/visto \
  && chown -R visto:visto /var/lib/visto
COPY --from=core-build /out/visto-core /usr/local/bin/visto-core
COPY --from=core-build /out/visto-server /usr/local/bin/visto-server
COPY --from=ffmpeg-runtime /runtime/out /opt/visto/ffmpeg
USER visto
ENV REVIEW_STUDIO_ADDR=0.0.0.0:8787 \
    REVIEW_STUDIO_DATA_DIR=/var/lib/visto \
    REVIEW_STUDIO_SOFTWARE_VIDEO_ENCODER=libopenh264 \
    PATH=/opt/visto/ffmpeg:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
VOLUME ["/var/lib/visto"]
EXPOSE 8787
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
  CMD ["/usr/local/bin/visto-server", "--address", "127.0.0.1:8787", "status"]
ENTRYPOINT ["/usr/local/bin/visto-core"]

FROM nginxinc/nginx-unprivileged:1.27-alpine@sha256:65e3e85dbaed8ba248841d9d58a899b6197106c23cb0ff1a132b7bfe0547e4c0 AS web
COPY infra/docker/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /workspace/apps/web/dist /usr/share/nginx/html
EXPOSE 8080
