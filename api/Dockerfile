FROM golang:1.18 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o cicy-code-api ./mgr/

FROM alpine:3.18
RUN apk add --no-cache ca-certificates tmux bash
WORKDIR /app
COPY --from=builder /app/cicy-code-api .
COPY static/ static/
COPY resources/ resources/
ENV PORT=8080
ENV GOMEMLIMIT=512MiB
EXPOSE 8080
CMD ["./cicy-code-api"]
