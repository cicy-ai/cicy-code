FROM alpine:3.19
COPY cicy-code-api /app/cicy-code-api
COPY static /app/static
WORKDIR /app
RUN mkdir -p logs
CMD ["./cicy-code-api"]
