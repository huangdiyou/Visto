<p align="center">
  <img src="docs/assets/visto-icon.svg" width="72" alt="Visto logo">
</p>

<h1 align="center">Visto</h1>

<p align="center">
  <a href="README.md">简体中文</a> · <strong>English</strong>
</p>

<p align="center"><strong>Keep media, versions, and feedback together in one project.</strong></p>

<p align="center">
  Local-first · Self-hosted · Media delivery and review
</p>

<p align="center">
  <a href="https://visto.akahxh.top/docs/en/">Read the user guide</a>
</p>

Visto Server is a free, open-source media collaboration platform for video production, design delivery, and teams that review work through multiple revisions. Deploy it on your own computer or server to organize project media, invite reviewers, collect feedback tied to specific moments or locations, and keep each version and its review decisions.

From the first draft to final approval, files, discussions, and delivery records stay with the project. Your deployment host and connected storage services hold the data.

![Visto external review: view an asset, mark a location, add comments, and submit a review decision](docs/assets/readme-review.png)

*Actual product interface in Chinese, shown with a fictional project and demo media.*

## Why Visto?

### Feedback points to the right place

Attach video comments to a timestamp or time range. Mark images with points, regions, and drawing annotations. Discussions refer to a selected version, so you can return to the exact frame or location when making changes.

### New versions keep the history

Upload revisions under the same asset while preserving earlier files, comments, and review decisions. Each review refers to a specific version, making it easier to check changes and revisit earlier decisions.

### Work with your team and external reviewers

Team members collaborate through project permissions. Clients and partners enter through review links. Set a password, expiration date, and commenting or download permissions to suit the delivery.

### Choose where your data lives

Use local storage on the deployment host, or connect WebDAV and S3-compatible services. Your deployment configuration determines where project data and media are stored. Remote storage and remote access use the services and networks you choose.

## From the first upload to final delivery

1. **Create a project:** choose storage, add members, and define the collaboration scope.
2. **Upload media:** add videos or images and wait for processing before previewing them.
3. **Start a review:** select a version and participants; create a share link when you need external feedback.
4. **Collect feedback:** comment on specific moments or locations, reply, and submit review decisions.
5. **Upload a revision:** add the revised file to the original asset and start the next review with the new version.
6. **Deliver and archive:** check delivery permissions, retain versions and discussions, and back up as needed.

Follow the [first project guide](https://visto.akahxh.top/docs/en/quick-start.html) to try the workflow with a demo asset.

## Features

| Capability | What it does |
| --- | --- |
| Projects and members | Organize media, members, and permissions by project |
| Images and videos | Upload, preview, track processing, and manage multiple versions of an asset |
| Visual feedback | Video timestamp and time-range comments; image points, regions, and drawing annotations |
| Review decisions | Set participants and approval conditions; record approval, requested changes, or rejection |
| Controlled sharing | Set passwords, expiration dates, and commenting or download permissions |
| Storage connections | Local managed storage, WebDAV, and S3-compatible storage |
| Instance maintenance | Host tools for updates, backups, restores, and rollbacks |

## Get started

Visto Server provides a browser workspace for team members and reviewers. Install Server on the deployment machine; using the workspace does not require the Desktop app.

| Deployment | Platform | Installation guide |
| --- | --- | --- |
| Native macOS | Apple Silicon | [macOS installation](https://visto.akahxh.top/docs/en/install-macos.html) |
| Native Windows | x64 | [Windows installation](https://visto.akahxh.top/docs/en/windows-install.html) |
| Native Linux | x86_64 / ARM64 with systemd | [Linux installation](https://visto.akahxh.top/docs/en/linux-install.html) |
| Docker | Linux amd64 / ARM64 | [Docker deployment](https://visto.akahxh.top/docs/en/docker.html) |

The user guide covers installation methods, downloads, system requirements, and video components. Read [installation preparation](https://visto.akahxh.top/docs/en/prepare.html) before choosing your platform.

The default address for a native installation is `http://127.0.0.1:8787/`. On first launch, create a workspace and Owner account, then configure storage, create a project, and invite members. Media processing depends on the media runtime; follow the guide to check that FFmpeg and ffprobe are ready after installation.

## User and maintenance guides

| What you want to do | Start here |
| --- | --- |
| Understand workspaces, projects, assets, and versions | [Understand Visto](https://visto.akahxh.top/docs/en/start.html) |
| Upload files and manage revisions | [Upload media](https://visto.akahxh.top/docs/en/upload.html) · [Version management](https://visto.akahxh.top/docs/en/versions.html) |
| Start reviews and collect client feedback | [Reviews and feedback](https://visto.akahxh.top/docs/en/reviews.html) · [External reviewer guide](https://visto.akahxh.top/docs/en/guest.html) |
| Connect storage and assign permissions | [Storage settings](https://visto.akahxh.top/docs/en/storage.html) · [Members and permissions](https://visto.akahxh.top/docs/en/members.html) |
| Update an instance and protect its data | [Updates, backups, and restores](https://visto.akahxh.top/docs/en/backup.html) |
| Resolve installation or runtime issues | [Troubleshooting](https://visto.akahxh.top/docs/en/troubleshooting.html) |

## Deployment and data

The Owner manages the workspace and global settings. Project leads and members collaborate according to project permissions. Give members individual accounts rather than sharing the Owner account.

Before exposing an instance to a local network or the internet, configure access, HTTPS, and permissions using the [network and security guide](https://visto.akahxh.top/docs/en/network.html). Share only the access needed for delivery. Archiving preserves project records; important data still needs a separate backup.

## Contribute

Suggestions, reproducible bug reports, and focused improvements are welcome. See the [contributing guide](CONTRIBUTING.md) for development checks and submission guidelines.

When reporting an issue, include the Visto version, operating system, reproduction steps, and error messages. Do not publish databases, original media, access tokens, or storage credentials. For vulnerabilities, follow the [security policy](SECURITY.md).

## License

Visto Server source code is licensed under the [Apache License 2.0](LICENSE). Third-party components such as FFmpeg retain their own licenses. See [NOTICE](NOTICE) and the third-party notices included with release packages.
