# Base images via espelho publico da AWS (public.ecr.aws/docker/library/*):
# sao as MESMAS imagens oficiais do Docker Hub, mas sem o rate limit de
# pulls anonimos (429 Too Many Requests) que derrubava o build no EasyPanel.
#
# Go 1.26+ e' OBRIGATORIO: whatsmeow e toda a familia golang.org/x declaram
# 'go 1.26.0' nos go.mod deles, e a imagem oficial vem com GOTOOLCHAIN=local
# (nao baixa toolchain novo sozinha). Com 1.25 o build morre logo no inicio
# com "go.mod requires go >= 1.26.0". Ao subir dependencia, confira se a
# diretiva 'go' do go.mod ainda cabe nesta imagem.
FROM public.ecr.aws/docker/library/golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev git

WORKDIR /app

# go.sum junto com o go.mod, de proposito: antes so' o go.mod era copiado, o
# 'go mod download' falhava em silencio (o '|| true' engolia o erro) e as
# dependencias so' eram resolvidas no 'go mod tidy' abaixo — sem go.sum,
# portanto sem verificacao de checksum e sem garantia de reproduzir as
# versoes testadas. Com os dois arquivos o download e' verificado, e esta
# camada fica em cache enquanto as dependencias nao mudarem.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# -mod=readonly: falha explicitamente se go.mod/go.sum nao baterem com o
# codigo, em vez de reescrever as dependencias durante o build (era o que o
# 'go mod tidy' fazia aqui). O que sobe pra producao passa a ser exatamente
# o conjunto de versoes commitado e testado.
RUN CGO_ENABLED=1 GOOS=linux go build -mod=readonly -ldflags="-w -s" -o connector ./cmd/server

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
