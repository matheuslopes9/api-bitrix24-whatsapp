# Base images via espelho publico da AWS (public.ecr.aws/docker/library/*):
# sao as MESMAS imagens oficiais do Docker Hub, mas sem o rate limit de
# pulls anonimos (429 Too Many Requests) que derrubava o build no EasyPanel.
FROM public.ecr.aws/docker/library/golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev git

WORKDIR /app

COPY go.mod ./
RUN go mod download || true

COPY . .
RUN go mod tidy && \
    CGO_ENABLED=1 GOOS=linux go build -ldflags="-w -s" -o connector ./cmd/server

FROM public.ecr.aws/docker/library/alpine:3.19

RUN apk add --no-cache ca-certificates sqlite-libs tzdata

WORKDIR /app

COPY --from=builder /app/connector .

RUN mkdir -p /app/sessions /app/media

# Diretorios que DEVEM persistir entre restarts do container:
# - /app/sessions: arquivos SQLite do whatsmeow (1 .db por numero QR conectado).
#   Sem volume persistente, toda recriacao do container faz cliente reescanear
#   o QR code do zero.
# - /app/media: midia inbound bufferizada antes do upload pro Bitrix.
#   Pode ser efemero, mas evita race se houver msg em transito durante restart.
#
# Em EasyPanel: aba "Mounts" do service, adicionar:
#   - Type: Volume, Name: uctalk-sessions, Mount path: /app/sessions
#   - Type: Volume, Name: uctalk-media,    Mount path: /app/media
# Em docker run: usar -v sessions_data:/app/sessions -v media_data:/app/media
VOLUME ["/app/sessions", "/app/media"]

EXPOSE 3000

CMD ["./connector"]
