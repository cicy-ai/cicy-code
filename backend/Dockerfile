FROM golang:1.22-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o cicy-dev-backend manager.go

FROM alpine:latest
RUN apk add --no-cache tmux
WORKDIR /app
COPY --from=build /app/cicy-dev-backend .
COPY --from=build /app/static ./static
EXPOSE 14444
CMD ["./cicy-dev-backend"]
