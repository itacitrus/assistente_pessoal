FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -o /bot .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
ENV TZ=America/Sao_Paulo

COPY --from=builder /bot /bot

ENTRYPOINT ["/bot"]
CMD ["run"]
