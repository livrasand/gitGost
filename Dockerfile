FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY . .

ARG COMMIT=unknown
ARG BUILD_TIME=unknown

RUN go build -ldflags="-X 'main.commitHash=${COMMIT}' -X 'main.buildTime=${BUILD_TIME}'" \
    -o app ./cmd/server


FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/app .
COPY web/ ./web/

CMD ["./app"]
