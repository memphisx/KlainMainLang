# Demo / verification build for ADR-00032's --static flag: proves a
# KlainMainLang-compiled binary can run in a `scratch` image with nothing
# else in it at all. Build from the repo root:
#
#   docker build -f docker/Dockerfile -t klainmainlang-demo .
#   docker run --rm klainmainlang-demo
#
# Three stages, matching the shape a real CI pipeline (building release
# binaries for multiple OS/architectures) would also use:
#   1. build the KlainMainLang compiler itself (plain Go, no libc concerns)
#   2. use that compiler + clang to statically compile docker/hello.ts
#   3. a scratch image containing nothing but the resulting static binary

# ---- Stage 1: build the KlainMainLang compiler ----
FROM golang:1.26-alpine AS compiler-builder
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY ast/ ast/
COPY codegen/ codegen/
COPY jsdoc/ jsdoc/
COPY lexer/ lexer/
COPY parser/ parser/
COPY resolver/ resolver/
RUN go build -o /out/KlainMainLang .

# ---- Stage 2: statically compile docker/hello.ts with that compiler ----
# Alpine/musl, not glibc: musl's static linking has none of glibc's
# NSS/getaddrinfo rough edges, and this demo program doesn't call fetch
# anyway (so libcurl's own static-linking requirements don't come up here).
FROM alpine:3.21 AS program-builder
RUN apk add --no-cache clang musl-dev
COPY --from=compiler-builder /out/KlainMainLang /usr/local/bin/KlainMainLang
COPY docker/hello.ts /src/hello.ts
WORKDIR /src
RUN KlainMainLang --static -o /src/hello hello.ts

# ---- Stage 3: nothing but the static binary ----
FROM scratch
COPY --from=program-builder /src/hello /hello
ENTRYPOINT ["/hello"]
