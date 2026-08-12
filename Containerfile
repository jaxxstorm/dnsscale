FROM docker.io/library/golang:1.24-alpine as builder
WORKDIR /go/src/dnsscale
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOARCH=$TARGETARCH go build

FROM alpine:3.22
COPY --from=builder /go/src/dnsscale/dnsscale /usr/local/bin
ENTRYPOINT dnsscale
