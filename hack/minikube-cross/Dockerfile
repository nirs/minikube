# Copyright 2025 The Kubernetes Authors All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
# TODO: Parmetrize k8s version and go version

FROM registry.k8s.io/build-image/kube-cross:v1.34.0-go1.24.6-bullseye.0

ARG ZIG_VERSION="0.15.1"
ARG ZIG_NAME="zig-x86_64-linux-${ZIG_VERSION}"

RUN curl --fail --silent --show-error "https://ziglang.org/download/${ZIG_VERSION}/${ZIG_NAME}.tar.xz" | tar xvJf - -C /opt

ENV PATH="/opt/${ZIG_NAME}:$PATH"
