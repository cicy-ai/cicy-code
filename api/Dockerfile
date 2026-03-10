FROM golang:1.22-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o cicy-code-api manager.go

FROM alpine:latest
RUN apk add --no-cache tmux
WORKDIR /app
COPY --from=build /app/cicy-code-api .
COPY --from=build /app/static ./static
EXPOSE 14444
CMD ["./cicy-code-api"]
