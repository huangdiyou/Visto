# Visto Server Third-Party Notices

This document accompanies the Visto Server distribution. The machine-readable
dependency inventory is included as `THIRD_PARTY.spdx.json` in each package.

## FFmpeg and media runtimes

The native Visto Server package does not bundle FFmpeg or ffprobe. Media
runtimes are installed separately and carry their own license, source,
build-record and third-party notice files. Do not infer an FFmpeg license,
source build or codec set from the Visto Server application version.

## Application dependencies

Visto Server is distributed under the Apache License 2.0. The package contains
the Visto license as `LICENSE.txt`. Direct and transitive dependency identities
and declared licenses are recorded in `THIRD_PARTY.spdx.json`.

## github.com/ulikunitz/xz v0.5.16

Pure Go XZ decoding used by the media runtime manager. BSD-3-Clause license:

Copyright (c) 2014-2022 Ulrich Kunitz
All rights reserved.

Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this
  list of conditions and the following disclaimer.
* Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.
* The copyright holder's name may not be used to endorse or promote products
  derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE.
