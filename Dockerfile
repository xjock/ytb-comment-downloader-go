# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/ytb-comment-downloader .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/ytb-comment-downloader /usr/local/bin/ytb-comment-downloader

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/ytb-comment-downloader"]
CMD ["serve", "--addr=:8080"]
