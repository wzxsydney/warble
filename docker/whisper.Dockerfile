# Builds whisper.cpp's HTTP server (whisper-server) from a pinned release.
# The official ghcr.io/ggml-org/whisper.cpp image ships the CLI, not the server,
# so we compile the server target ourselves.
FROM debian:bookworm-slim AS build
# clang (not gcc): gcc-12 on aarch64 fails to inline ggml's fp16 NEON intrinsics
# ("target specific option mismatch" on vfmaq_f16/vaddq_f16). clang builds them fine.
RUN apt-get update && apt-get install -y --no-install-recommends \
        git cmake make clang ca-certificates && rm -rf /var/lib/apt/lists/*
ARG WHISPER_TAG=v1.8.6
RUN git clone --depth 1 --branch ${WHISPER_TAG} \
        https://github.com/ggml-org/whisper.cpp /src
WORKDIR /src
# BUILD_SHARED_LIBS=OFF so whisper-server is self-contained (no libwhisper.so/
# libggml.so to copy). GGML_OPENMP=OFF avoids a libomp/libgomp runtime dependency;
# whisper.cpp still multithreads via its own thread pool.
# GGML_NATIVE=OFF is REQUIRED for a distributed binary: with it ON (the default),
# ggml compiles for the BUILD machine's exact CPU (-mcpu=native). The arm64 image
# is cross-built under QEMU in CI, so "native" bakes in instructions that crash
# (SIGILL) on other arm64 hardware, e.g. Docker Desktop's VM on Apple Silicon.
# OFF builds a portable baseline that runs everywhere; ggml still picks up common
# SIMD at runtime.
RUN cmake -B build -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++ \
        -DBUILD_SHARED_LIBS=OFF -DGGML_OPENMP=OFF -DGGML_NATIVE=OFF \
    && cmake --build build -j --config Release --target whisper-server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        libgomp1 curl && rm -rf /var/lib/apt/lists/*
# Binary location can vary across whisper.cpp versions; bin/ is current for v1.8.x.
COPY --from=build /src/build/bin/whisper-server /usr/local/bin/whisper-server
# Model is mounted at /models; args come from docker-compose.yml.
EXPOSE 8082
ENTRYPOINT ["whisper-server"]
