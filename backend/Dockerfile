ARG GO_VERSION=1.26
ARG SERVICE=gateway

FROM golang:${GO_VERSION} AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG SERVICE
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/service ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12
WORKDIR /
COPY --from=builder /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
