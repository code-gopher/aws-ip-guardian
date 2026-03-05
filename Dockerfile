# 构建阶段
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /ip-monitor ./cmd/

# 运行阶段
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

# 设置时区
ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder /ip-monitor .
COPY config.example.yaml ./config.example.yaml

ENTRYPOINT ["./ip-monitor"]
CMD ["--config", "/app/config.yaml"]
