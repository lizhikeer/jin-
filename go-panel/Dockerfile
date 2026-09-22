# 构建阶段：按 NAS 手册用国内代理拉依赖
FROM golang:1.26-bookworm AS builder
ENV GOPROXY=https://goproxy.cn,direct \
    CGO_ENABLED=0 \
    GOOS=linux
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/grabber ./cmd/grabber

# 运行阶段：时区已通过 time/tzdata 内嵌，仅需 CA 证书访问 HTTPS
FROM debian:bookworm-slim
RUN sed -i 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
ENV TZ=Asia/Shanghai \
    DATA_DIR=/app/data
WORKDIR /app
COPY --from=builder /out/grabber /app/grabber
VOLUME ["/app/data"]
EXPOSE 40000
CMD ["/app/grabber"]
