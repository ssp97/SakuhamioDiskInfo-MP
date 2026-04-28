FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /cdi-mp ./cmd/cdi-mp/

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /cdi-mp /usr/local/bin/cdi-mp

EXPOSE 8080

ENTRYPOINT ["cdi-mp"]
CMD ["-db", "/data/sakuhamio.db"]
