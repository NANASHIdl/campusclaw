# --- 构建阶段 -------------------------------------------------------------
FROM golang:1.24-alpine AS build

# 依赖已 vendor 进仓库，构建过程完全离线（无需访问 Go 代理）
ENV GOFLAGS=-mod=vendor \
    GOPROXY=off \
    CGO_ENABLED=0

WORKDIR /src

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# --- 运行阶段 -------------------------------------------------------------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=build /out/server /app/server

# 上传目录：仅挂载到本容器（Nginx 不将其作为静态目录）
RUN mkdir -p /app/uploads

EXPOSE 8080

ENTRYPOINT ["/app/server"]
