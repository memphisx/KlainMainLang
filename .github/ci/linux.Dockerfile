# The Linux CI lanes' toolchain, built once per change to this file
# (ci-image job, tagged by its hash) and run as each Linux job's container,
# so no job installs anything. Keep in step with
# .github/actions/toolchain/action.yml, which still sets up macOS and Windows.
FROM ubuntu:26.04

ARG GO_SERIES=1.26
ARG NODE_ORACLE=24.19.0
ARG NODE_FFI=26.10.0
ARG WASMTIME=50.0.1
ARG H2O=2.2.6

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get -o Acquire::Retries=5 update && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
      ca-certificates curl git make cmake xz-utils unzip pkg-config sudo zlib1g-dev \
      clang lld libcurl4-openssl-dev libgc-dev libtommath-dev libpcre2-dev \
      libssl-dev libnghttp2-dev libuv1-dev libc-ares-dev \
      libsqlite3-dev libgtk-3-dev libwebkit2gtk-4.1-dev \
 && rm -rf /var/lib/apt/lists/*

# klain:http's h2o backend: libh2o's libuv build (pkg-config libh2o), which
# Ubuntu stopped packaging after 24.04's libh2o-dev; built here from the same
# 2.2 series.
RUN curl -fsSL --retry 5 --retry-all-errors -o /tmp/h2o.tar.gz "https://github.com/h2o/h2o/archive/refs/tags/v${H2O}.tar.gz" \
 && mkdir /tmp/h2o && tar -xzf /tmp/h2o.tar.gz -C /tmp/h2o --strip-components=1 \
 && cd /tmp/h2o \
 && CC=clang cmake -DCMAKE_POLICY_VERSION_MINIMUM=3.5 -DWITH_MRUBY=OFF -DWITH_BUNDLED_SSL=OFF \
      -DBUILD_SHARED_LIBS=ON -DCMAKE_INSTALL_PREFIX=/usr/local . \
 && make -j"$(nproc)" libh2o \
 && cp -a libh2o.so* /usr/local/lib/ && cp -a include/. /usr/local/include/ \
 && mkdir -p /usr/local/lib/pkgconfig && cp libh2o.pc /usr/local/lib/pkgconfig/ \
 && ldconfig && cd / && rm -rf /tmp/h2o /tmp/h2o.tar.gz

# Go: the newest release of go.mod's series, as setup-go's go-version-file
# resolves it.
RUN arch=$(dpkg --print-architecture) \
 && curl -fsSL --retry 5 --retry-all-errors -o /tmp/go.json 'https://go.dev/dl/?mode=json&include=all' \
 && v=$(grep -o "\"version\": \"go${GO_SERIES}[.0-9]*\"" /tmp/go.json | head -1 | cut -d'"' -f4) && rm /tmp/go.json \
 && curl -fsSL --retry 5 --retry-all-errors "https://go.dev/dl/${v}.linux-${arch}.tar.gz" | tar -xz -C /usr/local
# Node: the general oracle on PATH, and node:ffi's oracle beside it.
RUN case $(dpkg --print-architecture) in amd64) a=x64 ;; arm64) a=arm64 ;; esac \
 && mkdir -p /opt/node /opt/node-ffi \
 && curl -fsSL --retry 5 --retry-all-errors "https://nodejs.org/dist/v${NODE_ORACLE}/node-v${NODE_ORACLE}-linux-${a}.tar.xz" | tar -xJ -C /opt/node --strip-components=1 \
 && curl -fsSL --retry 5 --retry-all-errors "https://nodejs.org/dist/v${NODE_FFI}/node-v${NODE_FFI}-linux-${a}.tar.xz" | tar -xJ -C /opt/node-ffi --strip-components=1
# WebAssembly's engine: the wasmtime C API.
RUN case $(dpkg --print-architecture) in amd64) a=x86_64 ;; arm64) a=aarch64 ;; esac \
 && mkdir -p /opt/wasmtime \
 && curl -fsSL --retry 5 --retry-all-errors "https://github.com/bytecodealliance/wasmtime/releases/download/v${WASMTIME}/wasmtime-v${WASMTIME}-${a}-linux-c-api.tar.xz" | tar -xJ -C /opt/wasmtime --strip-components=1

ENV PATH=/usr/local/go/bin:/opt/node/bin:$PATH \
    KML_FFI_NODE=/opt/node-ffi/bin/node \
    WASMTIME_DIR=/opt/wasmtime \
    KML_CI_IMAGE=1

# The jobs run as the runner's own uid (1001), not root: the file-permission
# tests expect EACCES. Its home holds the Go build cache.
RUN useradd -m -u 1001 -o ci && echo 'ci ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ci
ENV HOME=/home/ci
