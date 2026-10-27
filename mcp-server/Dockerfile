# --- 构建阶段 -------------------------------------------------------------
FROM golang:1.24-alpine AS build

# 只依赖标准库：不需要 vendor，也不需要访问 Go 代理。
ENV GOFLAGS=-mod=mod \
    GOPROXY=off \
    CGO_ENABLED=0

WORKDIR /src
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/mcp-server .

# --- 运行阶段 -------------------------------------------------------------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=build /out/mcp-server /app/mcp-server

EXPOSE 8090
ENTRYPOINT ["/app/mcp-server"]
