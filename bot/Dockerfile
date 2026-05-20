FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

# Copia toda a arvore do modulo (inclui subpacotes api/, llm/, synthesis/).
# O glob antigo `*.go` so pegava o package main raiz e quebrava o build apos
# a introducao dos subpacotes na feature de idosos.
COPY . .
RUN CGO_ENABLED=0 go build -o /bot .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
ENV TZ=America/Sao_Paulo

COPY --from=builder /bot /bot

ENTRYPOINT ["/bot"]
CMD ["run"]
